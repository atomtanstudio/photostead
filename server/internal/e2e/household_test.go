package e2e

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"

	"github.com/google/uuid"

	"photostead.com/server/internal/api"
)

// The Household: an invitation brings someone in, who sees only what's shared; sharing by hand and
// by an auto-share rule; anyone may take an item out (and a rule won't put it back), but only its
// owner can trash it; a rule turned off takes back what it alone shared.
func TestHousehold(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s, _, url, owner := signedIn(t, ctx)
	var h api.Household
	getJSON(t, owner, url+"/api/v1/household", &h)
	if h.Role != "owner" || !h.CanManage || len(h.Members) != 1 || h.Shared != 0 {
		t.Fatalf("household = %+v", h)
	}

	// An invitation, used once.
	var inv struct{ Token string }
	if code := doJSON(t, owner, "POST", url+"/api/v1/household/invites", `{"name":"Sam","role":"adult"}`, &inv); code != 201 || inv.Token == "" {
		t.Fatalf("invite = %d", code)
	}
	jar, _ := cookiejar.New(nil)
	sam := &http.Client{Jar: jar}
	var what struct{ Household, InvitedBy, Name string }
	getJSON(t, sam, url+"/api/v1/invites/"+inv.Token, &what)
	if what.Household != "Household" || what.Name != "Sam" || what.InvitedBy == "" {
		t.Errorf("invitation = %+v", what)
	}
	accept := mustJSON(map[string]string{"displayName": "Sam", "email": "sam@example.com", "password": "a long password for sam"})
	if code := status(t, sam, "POST", url+"/api/v1/invites/"+inv.Token, accept); code != 200 {
		t.Fatalf("accepting = %d", code)
	}
	if code := status(t, &http.Client{}, "POST", url+"/api/v1/invites/"+inv.Token, accept); code != 404 {
		t.Errorf("an invitation used twice = %d", code)
	}
	total := func(c *http.Client, library string) int64 {
		t.Helper()
		var b struct{ Total int64 }
		getJSON(t, c, url+"/api/v1/timeline/buckets?library="+library, &b)
		return b.Total
	}
	if n := total(sam, ""); n != 0 {
		t.Fatalf("Sam sees %d items before anything is shared", n)
	}
	all := total(owner, "")

	// Shared by hand: Sam sees them (and whose they are), can favorite them, can't trash them.
	var items []uuid.UUID
	rows, _ := s.Pool.Query(ctx, `SELECT a.id FROM assets a WHERE a.visibility = 'timeline' AND a.motion_of IS NULL AND a.source_type NOT IN ('ai_generated', 'ai_edited')
		AND NOT EXISTS (SELECT 1 FROM stack_assets sa WHERE sa.asset_id = a.id) ORDER BY a.id LIMIT 3`)
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		items = append(items, id)
	}
	rows.Close()
	var changed struct{ Changed int64 }
	if code := doJSON(t, owner, "POST", url+"/api/v1/household/share", mustJSON(map[string]any{"assetIds": items[:2]}), &changed); code != 200 || changed.Changed != 2 {
		t.Fatalf("share = %d %+v", code, changed)
	}
	if n := total(sam, ""); n != 2 {
		t.Errorf("Sam sees %d shared items", n)
	}
	var a api.Asset
	getJSON(t, sam, url+"/api/v1/assets/"+items[0].String(), &a)
	if a.Owner == nil || !a.Shared || !a.FromOthers {
		t.Errorf("a shared item, to Sam = owner %+v shared %v", a.Owner, a.Shared)
	}
	if code := status(t, sam, "GET", url+"/api/v1/assets/"+items[2].String(), ""); code != 404 {
		t.Errorf("an item not shared, to Sam = %d", code)
	}
	if code := doJSON(t, sam, "POST", url+"/api/v1/assets/favorite", mustJSON(map[string]any{"assetIds": items[:1], "favorite": true}), &changed); code != 200 || changed.Changed != 1 {
		t.Errorf("Sam favoriting = %d %+v", code, changed)
	}
	if code := doJSON(t, sam, "POST", url+"/api/v1/assets/trash", mustJSON(map[string]any{"assetIds": items[:1]}), &changed); code != 200 || changed.Changed != 0 {
		t.Errorf("Sam trashing another's item = %d %+v", code, changed)
	}
	if n, mine := total(owner, "household"), total(owner, "mine"); n != 2 || mine != all {
		t.Errorf("the owner's household %d, mine %d (all %d)", n, mine, all)
	}

	// Anyone may take an item out; a rule doesn't put it back.
	if code := doJSON(t, sam, "POST", url+"/api/v1/household/unshare", mustJSON(map[string]any{"assetIds": items[1:2]}), &changed); code != 200 || changed.Changed != 1 {
		t.Errorf("Sam taking one out = %d %+v", code, changed)
	}
	if code := doJSON(t, owner, "PUT", url+"/api/v1/household/rule", `{"enabled":true,"people":[]}`, &h); code != 200 || !h.Rule.Enabled {
		t.Fatalf("rule on = %d %+v", code, h.Rule)
	}
	var eligible int64
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM assets a WHERE a.visibility = 'timeline' AND a.motion_of IS NULL AND a.render_state = 'done'
		AND a.source_type NOT IN ('ai_generated', 'ai_edited')
		AND NOT EXISTS (SELECT 1 FROM stack_assets sk JOIN stacks st ON st.id = sk.stack_id WHERE sk.asset_id = a.id AND st.state = 'confirmed' AND st.cover_asset_id <> a.id)
		AND NOT EXISTS (SELECT 1 FROM asset_labels l WHERE l.asset_id = a.id AND l.label IN ('screenshot', 'document', 'receipt', 'id_card', 'qr_code'))`).Scan(&eligible)
	if h.Shared != int32(eligible-1) {
		t.Errorf("with the rule on, %d shared; want %d (all but the one taken out)", h.Shared, eligible-1)
	}
	var back bool
	_ = s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM space_assets WHERE asset_id = $1)`, items[1]).Scan(&back)
	if back {
		t.Error("the rule put back an item taken out")
	}

	// Off again: what it alone shared comes out; what was shared by hand stays.
	if code := doJSON(t, owner, "PUT", url+"/api/v1/household/rule", `{"enabled":false,"people":[]}`, &h); code != 200 || h.Shared != 1 {
		t.Errorf("rule off = %d, %d shared", code, h.Shared)
	}

	// Roles: only the owner (or admin) manages.
	if code := status(t, sam, "POST", url+"/api/v1/household/invites", `{"role":"adult"}`); code != 403 {
		t.Errorf("Sam inviting = %d", code)
	}
	var samID uuid.UUID
	for _, m := range h.Members {
		if m.Email == "sam@example.com" {
			samID = m.ID
		}
	}
	if code := doJSON(t, owner, "PATCH", url+"/api/v1/household/members/"+samID.String(), `{"role":"member"}`, &h); code != 200 {
		t.Errorf("setting Sam's role = %d", code)
	}
	if len(h.Members) != 2 || h.Members[1].Role != "member" {
		t.Errorf("members = %+v", h.Members)
	}
}
