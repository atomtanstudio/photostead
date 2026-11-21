package e2e

import (
	"context"
	"slices"
	"testing"
	"time"

	"photostead.com/server/internal/api"
)

// The setup checklist's own settings: steps skipped (only real ones, once each) and putting it
// away, kept with the person's other settings.
func TestSetupChecklistSettings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, _, url, c := signedIn(t, ctx)

	var me api.User
	if code := doJSON(t, c, "PATCH", url+"/api/v1/auth/me/settings", `{"setupSkipped":["copy","not-a-step","copy","phone"]}`, &me); code != 200 ||
		!slices.Equal(me.Settings.SetupSkipped, []string{"copy", "phone"}) || me.Settings.SetupHidden {
		t.Errorf("skipping steps = %d %+v", code, me.Settings)
	}
	if code := doJSON(t, c, "PATCH", url+"/api/v1/auth/me/settings", `{"setupHidden":true}`, &me); code != 200 || !me.Settings.SetupHidden {
		t.Errorf("putting it away = %d %+v", code, me.Settings)
	}
	getJSON(t, c, url+"/api/v1/auth/me", &me)
	if !me.Settings.SetupHidden || !slices.Equal(me.Settings.SetupSkipped, []string{"copy", "phone"}) {
		t.Errorf("after reading them back: %+v", me.Settings)
	}
	var after api.User
	if code := doJSON(t, c, "PATCH", url+"/api/v1/auth/me/settings", `{"setupSkipped":[]}`, &after); code != 200 || len(after.Settings.SetupSkipped) != 0 {
		t.Errorf("unskipping = %d %+v", code, after.Settings)
	}
}
