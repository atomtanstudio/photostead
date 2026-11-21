package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/api"
)

// The vault: a PIN is set, items go in and vanish from everything else, they're seen only while
// it's unlocked, wrong PINs make it wait, its trash is apart from the main one, items come back
// out, and stacks let go of what goes in.
func TestVault(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, owner, url, c := signedIn(t, ctx)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var items []uuid.UUID
	rows, _ := s.Pool.Query(ctx, `SELECT a.id FROM assets a WHERE a.type = 'image' AND a.motion_of IS NULL AND a.visibility = 'timeline'
		AND NOT EXISTS (SELECT 1 FROM stack_assets sa WHERE sa.asset_id = a.id) ORDER BY a.taken_at LIMIT 5`)
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		items = append(items, id)
	}
	rows.Close()
	if len(items) < 5 {
		t.Fatalf("%d photos", len(items))
	}
	type state struct {
		HasPIN   bool `json:"hasPin"`
		Unlocked bool
		Items    *int32
		Deleted  *int32
	}
	vault := func() state {
		t.Helper()
		var st state
		getJSON(t, c, url+"/api/v1/vault", &st)
		return st
	}
	total := func(collection string) (int64, int) {
		t.Helper()
		var b struct{ Total int64 }
		u := url + "/api/v1/timeline/buckets"
		if collection != "" {
			u += "?collection=" + collection
		}
		code := doJSON(t, c, "GET", u, "", &b)
		return b.Total, code
	}
	move := func(path string, ids ...uuid.UUID) (int64, int) {
		t.Helper()
		var out struct{ Changed int64 }
		code := doJSON(t, c, "POST", url+"/api/v1/vault/"+path, mustJSON(map[string]any{"assetIds": ids}), &out)
		return out.Changed, code
	}
	unlock := func(pin string) int {
		t.Helper()
		return status(t, c, "POST", url+"/api/v1/vault/unlock", mustJSON(map[string]string{"pin": pin}))
	}

	// No PIN yet: nothing can go in.
	if st := vault(); st.HasPIN || st.Unlocked {
		t.Fatalf("a new vault = %+v", st)
	}
	if _, code := move("add", items[0]); code != http.StatusConflict {
		t.Errorf("into a vault with no PIN = %d", code)
	}
	if code := status(t, c, "POST", url+"/api/v1/vault/pin", `{"pin":"12ab"}`); code != 422 {
		t.Errorf("a PIN with letters = %d", code)
	}
	var st state
	if code := doJSON(t, c, "POST", url+"/api/v1/vault/pin", `{"pin":"2468"}`, &st); code != 200 || !st.HasPIN || !st.Unlocked || *st.Items != 0 {
		t.Fatalf("setting the PIN = %d %+v", code, st)
	}

	// Two go in: gone from the timeline, there in the vault.
	before, _ := total("")
	if n, code := move("add", items[0], items[1]); code != 200 || n != 2 {
		t.Fatalf("into the vault = %d %d", code, n)
	}
	if after, _ := total(""); after != before-2 {
		t.Errorf("the timeline went from %d to %d", before, after)
	}
	if n, code := total("vault"); code != 200 || n != 2 {
		t.Errorf("the vault shows %d (%d)", n, code)
	}
	resp := do(t, c, "GET", url+"/api/v1/assets/"+items[0].String()+"/thumbnail", "", nil)
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("a vault thumbnail = %d, cached %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}

	// Locked: nothing of it can be seen.
	var locked state
	if code := doJSON(t, c, "POST", url+"/api/v1/vault/lock", "", &locked); code != 200 || locked.Unlocked || locked.Items != nil {
		t.Errorf("locking = %d %+v", code, locked)
	}
	if _, code := total("vault"); code != http.StatusForbidden {
		t.Errorf("a locked vault's items = %d", code)
	}
	for _, p := range []string{"", "/thumbnail", "/preview", "/original"} {
		if code := status(t, c, "GET", url+"/api/v1/assets/"+items[0].String()+p, ""); code != 404 {
			t.Errorf("GET a locked vault item%s = %d", p, code)
		}
	}
	if _, code := move("remove", items[0]); code != http.StatusForbidden {
		t.Errorf("out of a locked vault = %d", code)
	}

	// Wrong PINs: the fifth in a row makes it wait, even for the right one.
	for i := 1; i <= 5; i++ {
		want := http.StatusForbidden
		if i == 5 {
			want = http.StatusTooManyRequests
		}
		if code := unlock("0000"); code != want {
			t.Errorf("wrong PIN %d = %d", i, code)
		}
	}
	if code := unlock("2468"); code != http.StatusTooManyRequests {
		t.Errorf("the right PIN while waiting = %d", code)
	}
	exec(`UPDATE users SET vault_blocked_until = now() - interval '1 second' WHERE id = $1`, owner.ID)
	if code := unlock("2468"); code != 200 {
		t.Fatalf("unlocking = %d", code)
	}
	var failures int
	_ = s.Pool.QueryRow(ctx, `SELECT vault_failures FROM users WHERE id = $1`, owner.ID).Scan(&failures)
	if failures != 0 {
		t.Errorf("%d wrong tries still counted after the right one", failures)
	}

	// The vault's trash is its own: not in the main trash, not emptied with it.
	trashBefore, _ := total("trash")
	if code := doJSON(t, c, "POST", url+"/api/v1/assets/trash", mustJSON(map[string]any{"assetIds": []uuid.UUID{items[0]}}), nil); code != 200 {
		t.Fatalf("trashing a vault item = %d", code)
	}
	if n, _ := total("trash"); n != trashBefore {
		t.Errorf("a vault item shows in the main trash (%d, was %d)", n, trashBefore)
	}
	if n, _ := total("vault-deleted"); n != 1 {
		t.Errorf("the vault's trash shows %d", n)
	}
	var emptied struct{ Deleted int }
	_ = doJSON(t, c, "POST", url+"/api/v1/trash/empty", "{}", &emptied)
	if st = vault(); st.Deleted == nil || *st.Deleted != 1 {
		t.Errorf("emptying the main trash reached the vault's: %+v", st)
	}
	if code := doJSON(t, c, "POST", url+"/api/v1/vault/trash/empty", "{}", &emptied); code != 200 || emptied.Deleted != 1 {
		t.Errorf("emptying the vault's trash = %d %+v", code, emptied)
	}

	// Back out to the timeline.
	if n, code := move("remove", items[1]); code != 200 || n != 1 {
		t.Errorf("out of the vault = %d %d", code, n)
	}
	if n, _ := total(""); n != before-1 { // items[0] was deleted for good
		t.Errorf("after coming out, the timeline has %d (was %d)", n, before)
	}

	// A stack lets go of what goes in: a new top, and at one item left, no stack.
	var stack api.Stack
	if code := doJSON(t, c, "POST", url+"/api/v1/stacks", mustJSON(map[string]any{"assetIds": items[2:5]}), &stack); code != 201 {
		t.Fatalf("stacking = %d", code)
	}
	if _, code := move("add", stack.Cover); code != 200 {
		t.Fatalf("a stack's top into the vault = %d", code)
	}
	var left api.Stack
	getJSON(t, c, url+"/api/v1/assets/"+items[3].String()+"/stack", &left)
	if len(left.Members) != 2 || left.Cover == stack.Cover {
		t.Errorf("the stack after its top left = %+v", left)
	}
	if _, code := move("add", left.Cover); code != 200 {
		t.Fatal(code)
	}
	var n int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM stacks WHERE id = $1`, stack.ID).Scan(&n)
	if n != 0 {
		t.Error("a stack of one is left")
	}

	// Changing the PIN needs the current one, or the password.
	if code := status(t, c, "POST", url+"/api/v1/vault/pin", `{"pin":"1357"}`); code != http.StatusForbidden {
		t.Errorf("a new PIN with nothing else = %d", code)
	}
	if code := status(t, c, "POST", url+"/api/v1/vault/pin", `{"pin":"1357","currentPin":"2468"}`); code != 200 {
		t.Errorf("a new PIN with the current one = %d", code)
	}
	if code := status(t, c, "POST", url+"/api/v1/vault/pin", mustJSON(map[string]string{"pin": "9753", "password": adminPass})); code != 200 {
		t.Errorf("a new PIN with the password = %d", code)
	}
	if code := unlock("9753"); code != 200 {
		t.Errorf("the newest PIN = %d", code)
	}
}
