package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"photostead.com/server/internal/auth"
)

// The MCP server (/mcp, Streamable HTTP, stateless): a person's own Claude Code, Codex or other MCP
// client searches and organizes their library under their own AI subscription, and can add what it
// makes with the prompt attached. It authenticates with an API key (psk_…, `photostead apikey add`)
// or a paired device's token, and each tool calls the API itself, in-process, as that person: the
// visibility gate, their adult-content setting and the vault (always locked here) all hold.

// trustedViewer is who an in-process call from a tool is for; only the MCP server sets it.
type trustedViewer struct {
	id    uuid.UUID
	adult string
}

type trustedKey struct{}

// mcpCallerKey holds an MCP request's caller, for its tools.
type mcpCallerKey struct{}

// mcpCaller is the person an MCP request is from, and the Authorization it came with (for uploads,
// which check it themselves).
type mcpCaller struct {
	who    trustedViewer
	header string
}

func (s *server) mcpCallerOf(r *http.Request) (mcpCaller, bool) {
	header := r.Header.Get("Authorization")
	if id, _, ok := s.keyUser(r.Context(), header); ok {
		u, err := s.Store.Q.GetUser(r.Context(), id)
		if err != nil {
			return mcpCaller{}, false
		}
		var set Settings
		_ = json.Unmarshal(u.Settings, &set)
		return mcpCaller{trustedViewer{id, set.Adult}, header}, u.Role != "guest"
	}
	if t, ok := strings.CutPrefix(header, "Bearer "); ok && strings.HasPrefix(t, devicePrefix) {
		row, err := s.Store.Q.GetSessionUser(r.Context(), auth.TokenHash(t))
		if err != nil || row.Role == "guest" {
			return mcpCaller{}, false
		}
		var set Settings
		_ = json.Unmarshal(row.Settings, &set)
		return mcpCaller{trustedViewer{row.ID, set.Adult}, header}, true
	}
	return mcpCaller{}, false
}

// call runs one API request in-process as the caller, and decodes its JSON answer into out.
func (s *server) call(ctx context.Context, c mcpCaller, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequestWithContext(context.WithValue(ctx, trustedKey{}, c.who), method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		var e struct {
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if e.Detail == "" {
			e.Detail = http.StatusText(rec.Code)
		}
		return errors.New(e.Detail)
	}
	if out == nil {
		return nil
	}
	if b, ok := out.(*[]byte); ok {
		*b = rec.Body.Bytes()
		return nil
	}
	return json.Unmarshal(rec.Body.Bytes(), out)
}

// Item is an item as a tool lists it.
type mcpItem struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Date     string `json:"date" jsonschema:"the day it was taken (or made)"`
	AI       string `json:"ai,omitempty" jsonschema:"generated or edited, when made with AI (by evidence in the file)"`
	Favorite bool   `json:"favorite,omitempty"`
}

func items(list []TimelineAsset) []mcpItem {
	out := make([]mcpItem, len(list))
	for i, a := range list {
		out[i] = mcpItem{ID: a.ID.String(), Type: a.Type, Date: a.LocalDate, AI: a.AI, Favorite: a.Favorite}
	}
	return out
}

type (
	searchArgs struct {
		Query string `json:"query" jsonschema:"what to look for: what's in the picture, words in its prompt or text, a file name"`
		Type  string `json:"type,omitempty" jsonschema:"image or video, to keep to one"`
		Limit int    `json:"limit,omitempty" jsonschema:"how many to list, at most (default 50)"`
	}
	searchResult struct {
		Found int       `json:"found"`
		Items []mcpItem `json:"items" jsonschema:"best matches first"`
	}
	askArgs struct {
		Question string `json:"question" jsonschema:"a question about the library, e.g. how many videos of Ada did I make last year?"`
	}
	askResult struct {
		Answer string    `json:"answer"`
		Total  int       `json:"total"`
		Items  []mcpItem `json:"items"`
	}
	idArgs struct {
		ID string `json:"id" jsonschema:"an item's id"`
	}
	pictureArgs struct {
		ID    string `json:"id" jsonschema:"an item's id"`
		Large bool   `json:"large,omitempty" jsonschema:"the larger preview (1440 px) instead of the thumbnail"`
	}
	idsArgs struct {
		IDs []string `json:"ids" jsonschema:"items' ids"`
	}
	favoriteArgs struct {
		IDs      []string `json:"ids"`
		Favorite bool     `json:"favorite"`
	}
	keywordArgs struct {
		IDs    []string `json:"ids"`
		Add    []string `json:"add,omitempty" jsonschema:"keywords to put on them (nested with /, e.g. travel/japan)"`
		Remove []string `json:"remove,omitempty"`
	}
	describeArgs struct {
		IDs         []string `json:"ids"`
		Description string   `json:"description" jsonschema:"one description for all of them; empty clears it"`
	}
	albumArgs struct {
		Title string   `json:"title"`
		IDs   []string `json:"ids,omitempty" jsonschema:"items to put in it"`
	}
	addToAlbumArgs struct {
		Album string   `json:"album" jsonschema:"the album's id"`
		IDs   []string `json:"ids"`
	}
	addMediaArgs struct {
		FileName  string `json:"fileName" jsonschema:"with its extension, e.g. sunset.png"`
		Data      string `json:"data" jsonschema:"the file, base64"`
		Prompt    string `json:"prompt,omitempty" jsonschema:"what it was made from, for AI-made media"`
		Model     string `json:"model,omitempty" jsonschema:"the model that made it"`
		Tool      string `json:"tool,omitempty" jsonschema:"the service or app, e.g. an image API"`
		SourceURL string `json:"sourceUrl,omitempty"`
		Album     string `json:"album,omitempty" jsonschema:"an album to put it in, by title (made if there's none)"`
	}
	none   struct{}
	result struct {
		Changed int64 `json:"changed"`
	}
)

func uuids(ids []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, len(ids))
	for i, s := range ids {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("%q isn't an item's id", s)
		}
		out[i] = id
	}
	return out, nil
}

// mcpServer is the server for one request's caller: every tool acts as them.
func (s *server) mcpServer(c mcpCaller) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "photostead", Title: "Photostead", Version: Version}, &mcp.ServerOptions{
		Instructions: "Photostead is the person's own photo and video library. Search it, ask questions about it, look at pictures, " +
			"organize items (favorites, keywords, descriptions, albums) and add media you make with its prompt. Ids come from " +
			"search, ask and get_item. Pictures are downsized previews without location data.",
	})
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(srv, &mcp.Tool{Name: "search", Description: "Find items by what's in them, their prompt, text in the picture or file name.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, in searchArgs) (*mcp.CallToolResult, searchResult, error) {
			q := url.Values{"q": {in.Query}}
			if in.Type == "image" || in.Type == "video" {
				q.Set("type", in.Type)
			}
			var out searchOutput
			if err := s.call(ctx, c, http.MethodGet, "/api/v1/search?"+q.Encode(), nil, &out.Body); err != nil {
				return nil, searchResult{}, err
			}
			limit := in.Limit
			if limit <= 0 || limit > 500 {
				limit = 50
			}
			list := out.Body.Assets
			r := searchResult{Found: len(list)}
			r.Items = items(list[:min(limit, len(list))])
			return nil, r, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "ask", Description: "Ask a question about the library in plain language: counts, firsts and lasts, who, what, when.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, in askArgs) (*mcp.CallToolResult, askResult, error) {
			var out askOutput
			if err := s.call(ctx, c, http.MethodPost, "/api/v1/ask", map[string]any{"q": in.Question}, &out.Body); err != nil {
				return nil, askResult{}, err
			}
			list := out.Body.Assets
			return nil, askResult{Answer: out.Body.Answer, Total: out.Body.Total, Items: items(list[:min(50, len(list))])}, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "get_item", Description: "An item's details: when and where, people, keywords, description, albums, text in it, and how an AI item was made (prompt, model, LoRAs, settings).", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, in idArgs) (*mcp.CallToolResult, map[string]any, error) {
			id, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, nil, errors.New("that isn't an item's id")
			}
			var a Asset
			if err := s.call(ctx, c, http.MethodGet, "/api/v1/assets/"+id.String(), nil, &a); err != nil {
				return nil, nil, err
			}
			if a.Generation != nil {
				a.Generation.Settings = nil // large and mostly noise for a model
			}
			a.ThumbHash = nil
			return nil, asMap(a), nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "view_item", Description: "Look at an item: its thumbnail, or larger preview (a video's poster frame). No location data is in it.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, in pictureArgs) (*mcp.CallToolResult, none, error) {
			id, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, none{}, errors.New("that isn't an item's id")
			}
			size := "thumbnail"
			if in.Large {
				size = "preview"
			}
			var b []byte
			if err := s.call(ctx, c, http.MethodGet, "/api/v1/assets/"+id.String()+"/"+size, nil, &b); err != nil {
				return nil, none{}, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: b, MIMEType: "image/webp"}}}, none{}, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "library_counts", Description: "How many items, photos, videos, favorites, albums, people, AI-made items and so on there are.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, Counts, error) {
			var out Counts
			return nil, out, s.call(ctx, c, http.MethodGet, "/api/v1/library/counts", nil, &out)
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "list_albums", Description: "The albums: their ids, titles and how many items each holds.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, map[string]any, error) {
			var out struct {
				Albums []struct {
					ID    string `json:"id"`
					Title string `json:"title"`
					Kind  string `json:"kind"`
					Count int32  `json:"count"`
				} `json:"albums"`
			}
			if err := s.call(ctx, c, http.MethodGet, "/api/v1/albums", nil, &out); err != nil {
				return nil, nil, err
			}
			return nil, asMap(out), nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "list_people", Description: "The people (and pets) recognized in the library, with their ids and how many items each is in.", Annotations: ro},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, map[string]any, error) {
			var out struct {
				People []struct {
					ID    string `json:"id"`
					Name  string `json:"name"`
					Kind  string `json:"kind"`
					Count int32  `json:"count"`
				} `json:"people"`
			}
			if err := s.call(ctx, c, http.MethodGet, "/api/v1/people", nil, &out); err != nil {
				return nil, nil, err
			}
			named := out.People[:0]
			for _, p := range out.People {
				if p.Name != "" {
					named = append(named, p)
				}
			}
			out.People = named
			return nil, asMap(out), nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "set_favorite", Description: "Make items favorites, or not."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in favoriteArgs) (*mcp.CallToolResult, result, error) {
			ids, err := uuids(in.IDs)
			if err != nil {
				return nil, result{}, err
			}
			var out result
			return nil, out, s.call(ctx, c, http.MethodPost, "/api/v1/assets/favorite", map[string]any{"assetIds": ids, "favorite": in.Favorite}, &out)
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "edit_keywords", Description: "Put keywords on items, or take them off (only the person's own items change)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in keywordArgs) (*mcp.CallToolResult, map[string]any, error) {
			ids, err := uuids(in.IDs)
			if err != nil {
				return nil, nil, err
			}
			var out map[string]any
			return nil, out, s.call(ctx, c, http.MethodPost, "/api/v1/assets/keywords", map[string]any{"assetIds": ids, "add": in.Add, "remove": in.Remove}, &out)
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "set_description", Description: "Give items a description (only the person's own items change)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in describeArgs) (*mcp.CallToolResult, result, error) {
			ids, err := uuids(in.IDs)
			if err != nil {
				return nil, result{}, err
			}
			var out result
			return nil, out, s.call(ctx, c, http.MethodPost, "/api/v1/assets/description", map[string]any{"assetIds": ids, "text": in.Description}, &out)
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "create_album", Description: "Make an album, with items in it if given."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in albumArgs) (*mcp.CallToolResult, map[string]any, error) {
			ids, err := uuids(in.IDs)
			if err != nil {
				return nil, nil, err
			}
			var a struct {
				ID    uuid.UUID `json:"id"`
				Title string    `json:"title"`
			}
			if err := s.call(ctx, c, http.MethodPost, "/api/v1/albums", map[string]any{"title": in.Title}, &a); err != nil {
				return nil, nil, err
			}
			if len(ids) > 0 {
				if err := s.call(ctx, c, http.MethodPost, "/api/v1/albums/"+a.ID.String()+"/add", map[string]any{"assetIds": ids}, nil); err != nil {
					return nil, nil, err
				}
			}
			return nil, map[string]any{"id": a.ID.String(), "title": a.Title}, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "add_to_album", Description: "Put items in an album."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in addToAlbumArgs) (*mcp.CallToolResult, none, error) {
			ids, err := uuids(in.IDs)
			album, err2 := uuid.Parse(in.Album)
			if err != nil || err2 != nil {
				return nil, none{}, errors.New("album and ids must be ids")
			}
			return nil, none{}, s.call(ctx, c, http.MethodPost, "/api/v1/albums/"+album.String()+"/add", map[string]any{"assetIds": ids}, nil)
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "add_media", Description: "Add a picture or video to the library, such as one you made with an image API, with the prompt and model it was made with " +
		"(kept with it, searchable, and written to its sidecar). The same file twice is kept once."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in addMediaArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := base64.StdEncoding.DecodeString(in.Data)
			if err != nil || len(data) == 0 {
				return nil, nil, errors.New("data must be the file in base64")
			}
			out, err := s.mcpUpload(ctx, c, in, data)
			if err != nil {
				return nil, nil, err
			}
			var m map[string]any
			_ = json.Unmarshal(out, &m)
			return nil, m, nil
		})
	return srv
}

// mcpUpload sends a tool's file through the upload endpoint, as the caller, with what it says about it.
func (s *server) mcpUpload(ctx context.Context, c mcpCaller, in addMediaArgs, data []byte) (json.RawMessage, error) {
	var desc []string
	if in.Model != "" {
		desc = append(desc, "Model: "+in.Model)
	}
	if in.Tool != "" {
		desc = append(desc, "Made with "+in.Tool)
	}
	meta := map[string]any{"fileName": in.FileName, "takenAt": time.Now().UTC().Format(time.RFC3339), "prompt": in.Prompt,
		"description": strings.Join(desc, " · "), "sourceUrl": in.SourceURL, "album": in.Album}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	m, _ := json.Marshal(meta)
	if err := w.WriteField("meta", string(m)); err != nil {
		return nil, err
	}
	part, err := w.CreateFormFile("file", in.FileName)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/uploads", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", c.header)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		return nil, fmt.Errorf("the upload was refused (%d): %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	return rec.Body.Bytes(), nil
}

func (s *server) registerMCP(mux *http.ServeMux) {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		c, _ := r.Context().Value(mcpCallerKey{}).(mcpCaller)
		return s.mcpServer(c)
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 80 << 20})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if fromInternet(r.Context()) {
			http.NotFound(w, r)
			return
		}
		c, ok := s.mcpCallerOf(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="photostead"`)
			replyError(w, http.StatusUnauthorized, "Send an API key from `photostead apikey add` as Authorization: Bearer psk_…")
			return
		}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpCallerKey{}, c)))
	})
}

// asMap is a value as its JSON object, for tools whose answer has no fixed shape worth declaring.
func asMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}
