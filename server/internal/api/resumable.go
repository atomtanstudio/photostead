package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"photostead.com/server/internal/store/db"
)

// Resumable uploads, for phones and big videos (IETF draft-ietf-httpbis-resumable-upload-14,
// interop version 10). POST /api/v1/uploads/resumable makes an upload (Upload-Complete, and
// Upload-Length when known); its address comes back in Location, early in a 104 when the client
// speaks this version. PATCH there sends the next piece from Upload-Offset; HEAD says how much
// has arrived, to carry on after a dropped connection; DELETE gives up. What comes with the file
// goes in a Photostead-Meta header, base64 JSON (the multipart upload's meta part, sha256 included
// to check it), and the file name may be in Content-Disposition. The bytes so far are a partial
// file in the share's .uploads (its size is the offset). Once it's all there it's filed like any
// upload, with the same reply, kept to answer a retry, and the phone's name for it (deviceItem)
// is noted. An upload lasts three days.
//
// Through Cloudflare, Upload-Limit says max-append-size (pieceLimit): the app then sends a big file
// in pieces, and iOS's own uploads leave such files to it.
//
// iOS's own background uploads (Photos' upload jobs) speak an earlier version of the draft, so
// the earlier versions are understood too: Upload-Incomplete (Upload-Complete the other way
// round) instead of Upload-Complete, the 104 for whichever version the client says, any content
// type for a piece, and a POST with none of these headers is the whole file. Its upload of a
// phone's item (no hash, a deviceItem) is turned away at once (409) when Photostead has the item,
// or the app is sending it: iOS may have queued it before the app sent it.

const (
	resumablePath    = "/api/v1/uploads/resumable"
	resumableInterop = "10"
	resumableTTL     = 72 * time.Hour
	partialUpload    = "application/partial-upload"
	problemOffset    = "https://iana.org/assignments/http-problem-types#mismatching-upload-offset"
	problemLength    = "https://iana.org/assignments/http-problem-types#inconsistent-upload-length"
)

// inflight is an upload's lock, and how to end the request holding it: a connection that dropped
// without the server noticing yet keeps the lock until it's told to stop reading.
type inflight struct {
	mu    sync.Mutex
	amu   sync.Mutex
	abort func()
}

// hold takes an upload's lock for this request, first ending the request that has it.
func (s *server) hold(token string, w http.ResponseWriter) func() {
	v, _ := s.resumable.LoadOrStore(token, &inflight{})
	f := v.(*inflight)
	f.amu.Lock()
	if f.abort != nil {
		f.abort()
	}
	f.amu.Unlock()
	f.mu.Lock()
	rc := http.NewResponseController(w)
	f.amu.Lock()
	f.abort = func() { _ = rc.SetReadDeadline(time.Now()) }
	f.amu.Unlock()
	return func() {
		f.amu.Lock()
		f.abort = nil
		f.amu.Unlock()
		f.mu.Unlock()
	}
}

func (s *server) registerResumable(mux *http.ServeMux) {
	mux.HandleFunc("OPTIONS "+resumablePath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Upload-Limit", fmt.Sprintf("max-size=%d", maxUpload)+s.pieceLimit(r))
		w.WriteHeader(http.StatusOK) // iOS's background uploads look for a 200 here
	})
	mux.HandleFunc("POST "+resumablePath, s.createResumable)
	mux.HandleFunc("GET "+resumablePath+"/{token}", s.offsetResumable) // and HEAD
	mux.HandleFunc("PATCH "+resumablePath+"/{token}", s.appendResumable)
	mux.HandleFunc("DELETE "+resumablePath+"/{token}", s.cancelResumable)
}

func (s *server) createResumable(w http.ResponseWriter, r *http.Request) {
	owner, client, ok := s.member(r)
	if !ok {
		replyError(w, http.StatusUnauthorized, "Sign in, or send an API key (Authorization: Bearer psk_…).")
		return
	}
	complete, ok := uploadComplete(r.Header)
	if !ok && r.Header.Get("Upload-Draft-Interop-Version") == "" {
		complete, ok = true, true // not a resumable upload at all: the whole file, in one go
	}
	if !ok {
		replyError(w, http.StatusBadRequest, "Say whether this is the whole file: Upload-Complete: ?1 or ?0.")
		return
	}
	length, hasLength, ok := sfInt(r.Header.Get("Upload-Length"))
	if !ok {
		replyError(w, http.StatusBadRequest, "Upload-Length isn't a number.")
		return
	}
	if hasLength && length > maxUpload {
		replyError(w, http.StatusRequestEntityTooLarge, "That's bigger than an upload can be.")
		return
	}
	var meta UploadMeta
	if m := r.Header.Get("Photostead-Meta"); m != "" {
		b, err := base64.StdEncoding.DecodeString(m)
		if err != nil {
			b, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(m, "="))
		}
		if err != nil || json.Unmarshal(b, &meta) != nil {
			replyError(w, http.StatusBadRequest, "Photostead-Meta isn't base64 JSON.")
			return
		}
	}
	if meta.FileName == "" {
		if _, params, err := mime.ParseMediaType(r.Header.Get("Content-Disposition")); err == nil {
			meta.FileName = params["filename"]
		}
	}
	ctx := r.Context()
	if meta.DeviceItem != "" && meta.SHA256 == "" {
		known, _ := s.Store.Q.DeviceItemKnown(ctx, db.DeviceItemKnownParams{OwnerID: owner, Item: meta.DeviceItem})
		fromApp, _ := s.sendingNow(ctx, owner, []string{meta.DeviceItem}, true)
		if known || len(fromApp) > 0 {
			if !s.DiscardUploads {
				slog.Info("phone upload turned away: here already, or on its way from the app", "client", client, "file", meta.FileName)
			}
			replyError(w, http.StatusConflict, "Photostead has this already, or it's on its way.")
			return
		}
	}
	lib, ok := s.managedLibrary(ctx, w, owner)
	if !ok {
		return
	}
	staging := filepath.Join(lib.RootPath, ".uploads")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		replyError(w, http.StatusInternalServerError, err.Error())
		return
	}
	token := newLinkToken()
	partial := filepath.Join(staging, "resumable-"+token+".part")
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		replyError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f.Close()
	metaJSON, _ := json.Marshal(meta)
	sess, err := s.Store.Q.CreateUploadSession(ctx, db.CreateUploadSessionParams{Token: token, OwnerID: owner, Client: client, Meta: metaJSON,
		Length: pgtype.Int8{Int64: length, Valid: hasLength}, Path: partial, ExpiresAt: time.Now().Add(resumableTTL)})
	if err != nil {
		_ = os.Remove(partial)
		replyError(w, http.StatusInternalServerError, err.Error())
		return
	}
	release := s.hold(token, w)
	defer release()
	h := w.Header()
	h.Set("Location", resumablePath+"/"+token)
	h.Set("Upload-Limit", s.uploadLimit(r, sess))
	if v := r.Header.Get("Upload-Draft-Interop-Version"); v != "" {
		h.Set("Upload-Draft-Interop-Version", v)
		w.WriteHeader(104) // Upload Resumption Supported: where to carry on if this is cut short
	}
	s.write(w, r, sess, 0, complete, true)
}

func (s *server) session(w http.ResponseWriter, r *http.Request) (db.UploadSession, uuid.UUID, bool) {
	owner, _, ok := s.member(r)
	if !ok {
		replyError(w, http.StatusUnauthorized, "Sign in, or send an API key (Authorization: Bearer psk_…).")
		return db.UploadSession{}, owner, false
	}
	sess, err := s.Store.Q.UploadSession(r.Context(), db.UploadSessionParams{Token: r.PathValue("token"), OwnerID: owner})
	if errors.Is(err, pgx.ErrNoRows) {
		replyError(w, http.StatusNotFound, "No such upload (it may have run out: uploads last three days).")
		return sess, owner, false
	} else if err != nil {
		replyError(w, http.StatusInternalServerError, err.Error())
		return sess, owner, false
	}
	return sess, owner, true
}

// offset is how much of an upload has arrived: the partial file's size, or all of it once done.
func offset(sess db.UploadSession) (int64, error) {
	if sess.CompletedAt != nil {
		return sess.Length.Int64, nil
	}
	info, err := os.Stat(sess.Path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (s *server) offsetResumable(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := s.session(w, r)
	if !ok {
		return
	}
	defer s.hold(sess.Token, w)()
	n, err := offset(sess)
	if err != nil {
		s.lost(w, r, sess)
		return
	}
	h := w.Header()
	h.Set("Upload-Offset", strconv.FormatInt(n, 10))
	h.Set("Upload-Complete", sfBoolString(sess.CompletedAt != nil))
	h.Set("Upload-Incomplete", sfBoolString(sess.CompletedAt == nil)) // for the earlier drafts
	if sess.Length.Valid {
		h.Set("Upload-Length", strconv.FormatInt(sess.Length.Int64, 10))
	}
	h.Set("Upload-Limit", s.uploadLimit(r, sess))
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) appendResumable(w http.ResponseWriter, r *http.Request) {
	at, hasOffset, ok := sfInt(r.Header.Get("Upload-Offset"))
	complete, okComplete := uploadComplete(r.Header)
	if !ok || !hasOffset || !okComplete {
		replyError(w, http.StatusBadRequest, "Send Upload-Offset (where this piece starts) and Upload-Complete (?1 if it's the last).")
		return
	}
	sess, _, ok := s.session(w, r)
	if !ok {
		return
	}
	defer s.hold(sess.Token, w)()
	if sess.CompletedAt != nil { // a retry of the last piece, its reply lost: the same reply again
		if at == sess.Length.Int64 && complete {
			w.Header().Set("Upload-Complete", "?1")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(int(sess.ReplyStatus.Int32))
			_, _ = w.Write(sess.Reply)
			return
		}
		mismatch(w, sess.Length.Int64, at, true)
		return
	}
	n, err := offset(sess)
	if err != nil {
		s.lost(w, r, sess)
		return
	}
	if at != n {
		mismatch(w, n, at, false)
		return
	}
	if length, has, ok := sfInt(r.Header.Get("Upload-Length")); ok && has {
		if sess.Length.Valid && sess.Length.Int64 != length {
			s.inconsistent(w, r, sess)
			return
		}
		sess.Length = pgtype.Int8{Int64: length, Valid: true}
		_ = s.Store.Q.SetUploadLength(r.Context(), db.SetUploadLengthParams{ID: sess.ID, Length: sess.Length})
	}
	s.write(w, r, sess, n, complete, false)
}

func (s *server) cancelResumable(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := s.session(w, r)
	if !ok {
		return
	}
	defer s.hold(sess.Token, w)()
	_ = os.Remove(sess.Path)
	_ = s.Store.Q.DeleteUploadSession(r.Context(), sess.ID)
	s.resumable.Delete(sess.Token)
	w.WriteHeader(http.StatusNoContent)
}

// write appends the request's content to the upload, from at (with the upload's lock held).
func (s *server) write(w http.ResponseWriter, r *http.Request, sess db.UploadSession, at int64, complete, creating bool) {
	ctx := r.Context()
	room := int64(maxUpload) - at
	if sess.Length.Valid {
		room = sess.Length.Int64 - at
	}
	f, err := os.OpenFile(sess.Path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		s.lost(w, r, sess)
		return
	}
	n, err := io.Copy(f, io.LimitReader(r.Body, room+1))
	if serr := f.Sync(); err == nil {
		err = serr
	}
	f.Close()
	if n > room { // more than the file's length: this upload can't be right any more
		_ = os.Remove(sess.Path)
		_ = s.Store.Q.DeleteUploadSession(ctx, sess.ID)
		w.Header().Set("Upload-Complete", "?1")
		replyError(w, http.StatusRequestEntityTooLarge, "More arrived than the upload's length.")
		return
	}
	if err != nil { // cut short (or ended by a newer request): what arrived stays, from here
		return
	}
	end := at + n
	h := w.Header()
	if !complete {
		h.Set("Upload-Complete", "?0")
		h.Set("Upload-Incomplete", "?1")
		h.Set("Upload-Offset", strconv.FormatInt(end, 10))
		if creating {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}
	if sess.Length.Valid && end != sess.Length.Int64 {
		s.inconsistent(w, r, sess)
		return
	}
	sess.Length = pgtype.Int8{Int64: end, Valid: true}
	_ = s.Store.Q.SetUploadLength(ctx, db.SetUploadLengthParams{ID: sess.ID, Length: sess.Length})
	s.finish(w, r, sess)
}

// finish files a complete upload like any other, and keeps the reply for a retry.
func (s *server) finish(w http.ResponseWriter, r *http.Request, sess db.UploadSession) {
	ctx := r.Context()
	var meta UploadMeta
	_ = json.Unmarshal(sess.Meta, &meta)
	sum, err := fileSHA256(sess.Path)
	if err != nil {
		s.lost(w, r, sess)
		return
	}
	rec := &recorded{h: http.Header{}}
	if lib, ok := s.managedLibrary(ctx, rec, sess.OwnerID); ok {
		s.file(ctx, rec, sess.OwnerID, sess.Client, lib, sess.Path, sum, sess.Length.Int64, meta, nil)
	}
	_ = os.Remove(sess.Path) // still there unless it was filed: a duplicate, or refused
	if rec.status == 0 || rec.status == http.StatusAccepted {
		rec.status = http.StatusOK // filed (indexed soon): iOS's background uploads count a 202 as unfinished and send it all again
	}
	if meta.DeviceItem != "" && rec.status < 300 { // filed, or here already: either way the phone needn't send it again
		_ = s.Store.Q.RecordDeviceItem(ctx, db.RecordDeviceItemParams{OwnerID: sess.OwnerID, Item: meta.DeviceItem, Sha256: sum})
		sender := "the app"
		if meta.SHA256 == "" {
			sender = "iOS"
		}
		if !s.DiscardUploads { // a demo server keeps no file names
			slog.Info("phone upload", "client", sess.Client, "sent by", sender, "file", meta.FileName, "bytes", sess.Length.Int64, "status", rec.status)
		}
	}
	body := bytes.TrimSpace(rec.body.Bytes())
	if !json.Valid(body) {
		body = []byte("{}")
	}
	_ = s.Store.Q.CompleteUploadSession(ctx, db.CompleteUploadSessionParams{ID: sess.ID, ReplyStatus: pgtype.Int4{Int32: int32(rec.status), Valid: true}, Reply: body})
	w.Header().Set("Upload-Complete", "?1")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rec.status)
	_, _ = w.Write(body)
}

// lost: the partial file is gone, so the upload can't carry on.
func (s *server) lost(w http.ResponseWriter, r *http.Request, sess db.UploadSession) {
	_ = s.Store.Q.DeleteUploadSession(r.Context(), sess.ID)
	replyError(w, http.StatusNotFound, "This upload's data is gone; start it again.")
}

func (s *server) inconsistent(w http.ResponseWriter, r *http.Request, sess db.UploadSession) {
	_ = os.Remove(sess.Path)
	_ = s.Store.Q.DeleteUploadSession(r.Context(), sess.ID)
	w.Header().Set("Upload-Complete", "?1")
	problem(w, http.StatusBadRequest, map[string]any{"type": problemLength, "title": "inconsistent length values for upload"})
}

func mismatch(w http.ResponseWriter, expected, provided int64, complete bool) {
	w.Header().Set("Upload-Offset", strconv.FormatInt(expected, 10))
	w.Header().Set("Upload-Complete", sfBoolString(complete))
	problem(w, http.StatusConflict, map[string]any{"type": problemOffset, "title": "offset from request does not match offset of resource",
		"expected-offset": expected, "provided-offset": provided})
}

func problem(w http.ResponseWriter, status int, body map[string]any) {
	body["status"] = status
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *server) uploadLimit(r *http.Request, sess db.UploadSession) string {
	return fmt.Sprintf("max-size=%d, max-age=%d", maxUpload, max(0, int64(time.Until(sess.ExpiresAt).Seconds()))) + s.pieceLimit(r)
}

// cloudflarePiece is how much a client may send in one request when it reaches Photostead through
// Cloudflare (a Cloudflare Tunnel, or Cloudflare's proxy in front of a domain): Cloudflare turns
// away a request whose body is over 100 MB (on its Free and Pro plans), so a big video goes in
// pieces of 64 MiB.
const cloudflarePiece = 64 << 20

// pieceLimit is the Upload-Limit's max-append-size for this request, as ", max-append-size=N", or
// nothing: PHOTOSTEAD_UPLOAD_PIECE when it's set (a reverse proxy that caps requests), else 64 MiB
// through Cloudflare (requests from there carry a CF-Ray header). Elsewhere (at home, the tailnet,
// Funnel) a file goes in one request, which is quicker for iOS to send in the background.
func (s *server) pieceLimit(r *http.Request) string {
	switch {
	case s.UploadPiece > 0:
		return fmt.Sprintf(", max-append-size=%d", s.UploadPiece)
	case r.Header.Get("Cf-Ray") != "":
		return fmt.Sprintf(", max-append-size=%d", cloudflarePiece)
	}
	return ""
}

// sfBool reads a Structured Field boolean (?1 or ?0); ok is false when it's missing or isn't one.
func sfBool(v string) (bool, bool) {
	switch strings.TrimSpace(strings.SplitN(v, ";", 2)[0]) {
	case "?1":
		return true, true
	case "?0":
		return false, true
	}
	return false, false
}

// uploadComplete reads whether a request carries the rest of the file: Upload-Complete, or the
// earlier drafts' Upload-Incomplete.
func uploadComplete(h http.Header) (complete, ok bool) {
	if c, ok := sfBool(h.Get("Upload-Complete")); ok {
		return c, true
	}
	if inc, ok := sfBool(h.Get("Upload-Incomplete")); ok {
		return !inc, true
	}
	return false, false
}

func sfBoolString(b bool) string {
	if b {
		return "?1"
	}
	return "?0"
}

// sfInt reads a Structured Field non-negative integer: has is false when the field is missing,
// ok false when it's there but isn't one.
func sfInt(v string) (n int64, has, ok bool) {
	v = strings.TrimSpace(strings.SplitN(v, ";", 2)[0])
	if v == "" {
		return 0, false, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 || len(v) > 15 {
		return 0, true, false
	}
	return n, true, true
}

func fileSHA256(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// recorded keeps a response to send later (and keep).
type recorded struct {
	h      http.Header
	status int
	body   bytes.Buffer
}

func (r *recorded) Header() http.Header { return r.h }
func (r *recorded) WriteHeader(c int) {
	if r.status == 0 {
		r.status = c
	}
}
func (r *recorded) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(b)
}
