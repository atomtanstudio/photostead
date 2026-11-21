package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/api"
	"photostead.com/server/internal/jobs"
)

type syncRead struct {
	types  []string
	assets map[uuid.UUID]api.SyncAsset
	gone   map[uuid.UUID]bool
	albums map[uuid.UUID]api.SyncAlbum
	cursor string
}

func readSync(t *testing.T, c *http.Client, url, since string) syncRead {
	t.Helper()
	u := url + "/api/v1/sync/stream"
	if since != "" {
		u += "?since=" + since
	}
	resp, err := c.Get(u)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("sync = %v %v", resp, err)
	}
	defer resp.Body.Close()
	out := syncRead{assets: map[uuid.UUID]api.SyncAsset{}, gone: map[uuid.UUID]bool{}, albums: map[uuid.UUID]api.SyncAlbum{}}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var l struct {
			Type   string
			Asset  *api.SyncAsset
			Album  *api.SyncAlbum
			ID     uuid.UUID
			Cursor string
		}
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("a line that isn't JSON: %s", sc.Text())
		}
		out.types = append(out.types, l.Type)
		switch l.Type {
		case "asset":
			out.assets[l.Asset.ID] = *l.Asset
		case "assetGone":
			out.gone[l.ID] = true
		case "album":
			out.albums[l.Album.ID] = *l.Album
		case "checkpoint":
			out.cursor = l.Cursor
		}
	}
	if len(out.types) == 0 || out.types[len(out.types)-1] != "checkpoint" || out.cursor == "" {
		t.Fatalf("the feed didn't end with a checkpoint: %v", out.types)
	}
	return out
}

// The sync feed: everything at first, then only what changed (an item's stars, an album, the
// trash, the Household), a change that commits after a read still comes next time, and a cursor
// that's too old or from ahead starts over.
func TestSyncFeed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, owner, url, c := signedIn(t, ctx)

	first := readSync(t, c, url, "")
	var visible int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM visible_assets($1)`, owner.ID).Scan(&visible)
	if len(first.assets) != visible || visible == 0 {
		t.Fatalf("the first read sent %d items of %d", len(first.assets), visible)
	}
	var x, y, z uuid.UUID
	for id, a := range first.assets {
		if a.SHA256 == "" || !a.InLibrary || a.FileName == "" {
			t.Fatalf("an item is missing what a phone needs: %+v", a)
		}
		if a.MotionOf == nil && !a.Stacked && a.Type == "image" {
			switch {
			case x == uuid.Nil:
				x = id
			case y == uuid.Nil:
				y = id
			case z == uuid.Nil:
				z = id
			}
		}
	}

	// Nothing changed: nothing but the checkpoint.
	same := readSync(t, c, url, first.cursor)
	if len(same.assets)+len(same.gone)+len(same.albums) != 0 {
		t.Errorf("with nothing changed, it sent %v", same.types)
	}

	// A favorite and an album.
	if _, err := s.Pool.Exec(ctx, `INSERT INTO asset_user_state (asset_id, user_id, favorite) VALUES ($1, $2, true)`, x, owner.ID); err != nil {
		t.Fatal(err)
	}
	var album struct{ ID uuid.UUID }
	if code := doJSON(t, c, "POST", url+"/api/v1/albums", mustJSON(map[string]any{"title": "Trip", "assetIds": []uuid.UUID{y, x}}), &album); code != 201 {
		t.Fatalf("album = %d", code)
	}
	next := readSync(t, c, url, same.cursor)
	if !next.assets[x].Favorite || len(next.assets) != 2 {
		t.Errorf("after a favorite and an album: %d items, x favorite %v", len(next.assets), next.assets[x].Favorite)
	}
	if al := next.albums[album.ID]; al.Title != "Trip" || al.Role != "owner" || len(al.AssetIDs) != 2 {
		t.Errorf("the album = %+v", al)
	}

	// A change that commits after a read comes next time.
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO asset_user_state (asset_id, user_id, rating) VALUES ($1, $2, 4)`, z, owner.ID); err != nil {
		t.Fatal(err)
	}
	during := readSync(t, c, url, next.cursor)
	if _, ok := during.assets[z]; ok {
		t.Errorf("an uncommitted change was sent")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	after := readSync(t, c, url, during.cursor)
	if after.assets[z].Rating != 4 {
		t.Errorf("a change committed after a read never came: %v", after.types)
	}

	// Found adult by a scan: it comes again, marked; its owner saying it isn't: again, unmarked. A
	// score that changes alone, or another label, isn't a change.
	if _, err := s.Pool.Exec(ctx, `INSERT INTO asset_labels (asset_id, label, basis, score) VALUES ($1, 'adult', 'visual', 0.97)`, z); err != nil {
		t.Fatal(err)
	}
	adult := readSync(t, c, url, after.cursor)
	if !adult.assets[z].Adult || len(adult.assets) != 1 {
		t.Errorf("after a scan found it adult: %v, adult %v", adult.types, adult.assets[z].Adult)
	}
	for _, q := range []string{`UPDATE asset_labels SET score = 0.99 WHERE asset_id = $1 AND label = 'adult'`,
		`INSERT INTO asset_labels (asset_id, label, basis) VALUES ($1, 'screenshot', 'visual')`} {
		if _, err := s.Pool.Exec(ctx, q, z); err != nil {
			t.Fatal(err)
		}
	}
	if quiet := readSync(t, c, url, adult.cursor); len(quiet.assets) != 0 {
		t.Errorf("a new score or another label was sent: %v", quiet.types)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE asset_labels SET basis = 'rejected' WHERE asset_id = $1 AND label = 'adult'`, z); err != nil {
		t.Fatal(err)
	}
	notAdult := readSync(t, c, url, adult.cursor)
	if notAdult.assets[z].Adult || len(notAdult.assets) != 1 {
		t.Errorf("after its owner said it isn't adult: %v", notAdult.types)
	}

	// Trashed: gone (from its albums too, on the phone: they aren't sent again for it).
	if _, err := s.Pool.Exec(ctx, `UPDATE assets SET trashed_at = now() WHERE id = $1`, y); err != nil {
		t.Fatal(err)
	}
	trashed := readSync(t, c, url, notAdult.cursor)
	if !trashed.gone[y] || len(trashed.assets) != 0 {
		t.Errorf("after the trash: %v", trashed.types)
	}

	// Sam, in the household: only what's shared with it, arriving and leaving.
	var inv struct{ Token string }
	doJSON(t, c, "POST", url+"/api/v1/household/invites", `{"role":"adult"}`, &inv)
	jar, _ := cookiejar.New(nil)
	sam := &http.Client{Jar: jar}
	if code := status(t, sam, "POST", url+"/api/v1/invites/"+inv.Token, mustJSON(map[string]string{"displayName": "Sam", "email": "sam@example.com", "password": "a long password for sam"})); code != 200 {
		t.Fatalf("Sam joining = %d", code)
	}
	samFirst := readSync(t, sam, url, "")
	if len(samFirst.assets) != 0 {
		t.Errorf("Sam sees %d items before anything is shared", len(samFirst.assets))
	}
	doJSON(t, c, "POST", url+"/api/v1/household/share", mustJSON(map[string]any{"assetIds": []uuid.UUID{x}}), nil)
	samNext := readSync(t, sam, url, samFirst.cursor)
	if a := samNext.assets[x]; !a.Shared || !a.InLibrary || a.Favorite || a.OwnerID != owner.ID {
		t.Errorf("shared with the household, Sam sees %+v", a)
	}
	doJSON(t, c, "POST", url+"/api/v1/household/unshare", mustJSON(map[string]any{"assetIds": []uuid.UUID{x}}), nil)
	if samGone := readSync(t, sam, url, samNext.cursor); !samGone.gone[x] {
		t.Errorf("unshared, Sam's feed says %v", samGone.types)
	}

	// From ahead (a database restored from a backup) or too old (cleared away): start over.
	ahead := readSync(t, c, url, "999999999999")
	if ahead.types[0] != "reset" || len(ahead.assets) != visible-1 {
		t.Errorf("from ahead: %s first, %d items", ahead.types[0], len(ahead.assets))
	}
	if err := jobs.PruneSync(ctx, s.Q, time.Now().Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if old := readSync(t, c, url, first.cursor); old.types[0] != "reset" {
		t.Errorf("after clearing, an old cursor gets %s first", old.types[0])
	}
	fresh := readSync(t, c, url, "")
	if latest := readSync(t, c, url, fresh.cursor); latest.types[0] != "checkpoint" {
		t.Errorf("after clearing, a cursor from after it got %v", latest.types)
	}
}
