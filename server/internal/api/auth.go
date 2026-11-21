package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"photostead.com/server/internal/auth"
	"photostead.com/server/internal/jobs"
	"photostead.com/server/internal/store/db"
)

type User struct {
	ID             uuid.UUID `json:"id"`
	Email          string    `json:"email"`
	Username       string    `json:"username"`
	DisplayName    string    `json:"displayName"`
	Role           string    `json:"role" enum:"admin,member,guest"`
	TimeZone       string    `json:"timeZone" doc:"IANA time zone that dates follow, e.g. America/Los_Angeles"`
	TimeZoneChosen bool      `json:"timeZoneChosen" doc:"False until the person has picked a zone; until then the server's is used"`
	Settings       Settings  `json:"settings"`
}

// Settings are a person's own choices, kept on the server so they follow them between devices.
type Settings struct {
	Organize     bool     `json:"organize" doc:"Show and use star ratings, pick/reject flags and color labels (off by default: favorites are enough for most)"`
	HideMemories bool     `json:"hideMemories" doc:"Don't show memories above the timeline"`
	MemoriesAI   bool     `json:"memoriesAi" doc:"Let AI-made images into memories (left out by default)"`
	Adult        string   `json:"adult,omitempty" enum:"show,blur,hide" doc:"Adult content (nudity or sex): shown (the default), blurred until clicked, or hidden from the timeline, albums, collections and search. The Adult collection always shows it"`
	SetupHidden  bool     `json:"setupHidden,omitempty" doc:"The admin put the setup checklist away (it's back in the sidebar while steps are left)"`
	SetupSkipped []string `json:"setupSkipped,omitempty" doc:"Setup steps the admin skipped, or did elsewhere (a second copy of the photos)"`
}

// setupSteps are the setup checklist's steps (the web app's Setup), for setupSkipped.
var setupSteps = []string{"photos", "phone", "away", "household", "copy"}

func (s *server) userOf(u db.User) User {
	var set Settings
	_ = json.Unmarshal(u.Settings, &set)
	return User{ID: u.ID, Email: shownEmail(u.Email), Username: u.Username, DisplayName: u.DisplayName, Role: u.Role,
		TimeZone: auth.EffectiveZone(u, s.ServerZone), TimeZoneChosen: u.TimeZone.Valid && u.TimeZone.String != "", Settings: set}
}

type setupStatus struct {
	Body struct {
		Needed     bool   `json:"needed" doc:"True until the first account exists"`
		ServerZone string `json:"serverZone" doc:"The server's time zone, offered as a default"`
		SignOn     string `json:"signOn,omitempty" doc:"With single sign-on set up: the name for its button (sign in at /api/v1/auth/oidc/start)"`
		Internet   bool   `json:"internet,omitempty" doc:"Reached through Funnel, from the internet: only guests can sign in here"`
	}
}

type setupInput struct {
	Body struct {
		DisplayName string `json:"displayName" minLength:"1" maxLength:"100"`
		Email       string `json:"email" format:"email" maxLength:"320"`
		Password    string `json:"password" minLength:"12" maxLength:"1024"`
		TimeZone    string `json:"timeZone" minLength:"1" maxLength:"64" example:"America/Los_Angeles"`
	}
}

type timeZoneInput struct {
	Body struct {
		TimeZone string `json:"timeZone" minLength:"1" maxLength:"64" example:"Europe/London"`
	}
}

type timeZoneOutput struct {
	Body struct {
		User              User  `json:"user"`
		CameraClocksMoved int64 `json:"cameraClocksMoved" doc:"Items whose camera time now reads in the new zone"`
		DaysChanged       int64 `json:"daysChanged" doc:"Items that moved to a different day"`
	}
}

type loginInput struct {
	Body struct {
		Email    string `json:"email" format:"email" maxLength:"320"`
		Password string `json:"password" minLength:"1" maxLength:"1024"`
	}
}

type loginOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      User
}

// LoginResult is a password sign-in: done (the user), or waiting for a two-step code.
type LoginResult struct {
	User    *User  `json:"user,omitempty"`
	TwoStep bool   `json:"twoStep,omitempty" doc:"A code from the authenticator app (or a recovery code) is needed next: POST /api/v1/auth/login/two-step with the ticket"`
	Ticket  string `json:"ticket,omitempty" doc:"For the second step; good for 5 minutes and 5 tries"`
}

type passwordLoginOutput struct {
	SetCookie *http.Cookie `header:"Set-Cookie"`
	Body      LoginResult
}

type logoutInput struct {
	Session string `cookie:"ps_session"`
}

type logoutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

type meOutput struct{ Body User }

type settingsInput struct {
	Body struct {
		Organize     *bool     `json:"organize,omitempty"`
		HideMemories *bool     `json:"hideMemories,omitempty"`
		MemoriesAI   *bool     `json:"memoriesAi,omitempty"`
		Adult        *string   `json:"adult,omitempty" enum:"show,blur,hide"`
		SetupHidden  *bool     `json:"setupHidden,omitempty"`
		SetupSkipped *[]string `json:"setupSkipped,omitempty" maxItems:"10" doc:"photos, phone, away, household, copy"`
	}
}

func (s *server) registerAuth() {
	huma.Register(s.api, huma.Operation{
		OperationID: "setupStatus", Method: http.MethodGet, Path: "/api/v1/setup", Tags: []string{"Setup"},
		Summary: "Whether this server still needs its first account",
	}, func(ctx context.Context, _ *struct{}) (*setupStatus, error) {
		n, err := s.Store.Q.CountUsers(ctx)
		if err != nil {
			return nil, err
		}
		out := &setupStatus{}
		out.Body.Needed, out.Body.ServerZone = n == 0, s.ServerZone
		if s.OIDC != nil && s.OIDC.Config.Enabled() {
			out.Body.SignOn = s.OIDC.Config.Name
		}
		if fromInternet(ctx) { // guests only: no first account, no single sign-on
			out.Body.Needed, out.Body.SignOn, out.Body.Internet = false, "", true
		}
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "setup", Method: http.MethodPost, Path: "/api/v1/setup", Tags: []string{"Setup"},
		Summary:     "Create the first account",
		Description: "Only works while the server has no accounts. Signs the new account in.",
	}, func(ctx context.Context, in *setupInput) (*loginOutput, error) {
		u, err := auth.Setup(ctx, s.Store, in.Body.DisplayName, in.Body.Email, in.Body.Password, in.Body.TimeZone)
		if errors.Is(err, auth.ErrAlreadySetUp) {
			return nil, huma.Error409Conflict(err.Error())
		}
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		token, err := auth.StartSession(ctx, s.Store, u)
		if err != nil {
			return nil, err
		}
		if s.Jobs != nil { // their folder in the photostead share, and the folders mounted to be indexed
			_, _ = s.Jobs.Insert(ctx, jobs.DiscoverArgs{}, nil)
		}
		return &loginOutput{SetCookie: s.cookie(ctx, token, auth.SessionTTL), Body: s.userOf(u)}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "setTimeZone", Method: http.MethodPut, Path: "/api/v1/auth/me/time-zone", Tags: []string{"Auth"},
		Summary:     "Choose the time zone dates follow",
		Description: "Re-dates the library: camera clocks keep their time in the new zone, and moments without a recorded offset move to the day they fall on there.",
		Security:    sessionAuth,
	}, func(ctx context.Context, in *timeZoneInput) (*timeZoneOutput, error) {
		u, err := s.Store.Q.GetUser(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		ch, err := auth.SetTimeZone(ctx, s.Store, u, in.Body.TimeZone, s.ServerZone)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		if u, err = s.Store.Q.GetUser(ctx, u.ID); err != nil {
			return nil, err
		}
		out := &timeZoneOutput{}
		out.Body.User, out.Body.CameraClocksMoved, out.Body.DaysChanged = s.userOf(u), ch.CameraClocksMoved, ch.DaysChanged
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "setSettings", Method: http.MethodPatch, Path: "/api/v1/auth/me/settings", Tags: []string{"Auth"},
		Summary: "Change your own settings", Security: sessionAuth,
	}, func(ctx context.Context, in *settingsInput) (*meOutput, error) {
		for name, v := range map[string]*bool{"organize": in.Body.Organize, "hideMemories": in.Body.HideMemories, "memoriesAi": in.Body.MemoriesAI,
			"setupHidden": in.Body.SetupHidden} {
			if v == nil {
				continue
			}
			if err := s.Store.Q.SetUserSetting(ctx, db.SetUserSettingParams{ID: viewer(ctx), Name: name, Value: []byte(strconv.FormatBool(*v))}); err != nil {
				return nil, err
			}
		}
		if v := in.Body.Adult; v != nil {
			b, _ := json.Marshal(*v)
			if err := s.Store.Q.SetUserSetting(ctx, db.SetUserSettingParams{ID: viewer(ctx), Name: "adult", Value: b}); err != nil {
				return nil, err
			}
		}
		if v := in.Body.SetupSkipped; v != nil {
			steps := []string{}
			for _, step := range *v {
				if slices.Contains(setupSteps, step) && !slices.Contains(steps, step) {
					steps = append(steps, step)
				}
			}
			b, _ := json.Marshal(steps)
			if err := s.Store.Q.SetUserSetting(ctx, db.SetUserSettingParams{ID: viewer(ctx), Name: "setupSkipped", Value: b}); err != nil {
				return nil, err
			}
		}
		u, err := s.Store.Q.GetUser(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		return &meOutput{Body: s.userOf(u)}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "login", Method: http.MethodPost, Path: "/api/v1/auth/login", Tags: []string{"Auth"},
		Summary:     "Sign in",
		Description: "Checks an email and password and starts a session (a cookie), or, with two-step sign-in on, gives a ticket for the code that comes next.",
	}, func(ctx context.Context, in *loginInput) (*passwordLoginOutput, error) {
		u, err := auth.Password(ctx, s.Store, in.Body.Email, in.Body.Password)
		if errors.Is(err, auth.ErrBadLogin) {
			return nil, huma.Error401Unauthorized(err.Error())
		}
		if err != nil {
			return nil, err
		}
		if err := mayStart(ctx, u); err != nil {
			return nil, err
		}
		out := &passwordLoginOutput{}
		if on, err := auth.TwoStepOn(ctx, s.Store.Q, u.ID); err != nil {
			return nil, err
		} else if on {
			out.Body.TwoStep = true
			out.Body.Ticket, err = auth.StartFlow(ctx, s.Store.Q, "second_step", uuid.NullUUID{UUID: u.ID, Valid: true}, struct{}{}, 5*time.Minute)
			return out, err
		}
		token, err := auth.StartSession(ctx, s.Store, u)
		if err != nil {
			return nil, err
		}
		c, user := s.cookie(ctx, token, auth.SessionTTL), s.userOf(u)
		out.SetCookie, out.Body.User = &c, &user
		return out, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "logout", Method: http.MethodPost, Path: "/api/v1/auth/logout", Tags: []string{"Auth"},
		Summary: "Sign out",
	}, func(ctx context.Context, in *logoutInput) (*logoutOutput, error) {
		if in.Session != "" {
			_ = s.Store.Q.DeleteSession(ctx, auth.TokenHash(in.Session))
		}
		return &logoutOutput{SetCookie: s.cookie(ctx, "", -1)}, nil
	})

	huma.Register(s.api, huma.Operation{
		OperationID: "me", Method: http.MethodGet, Path: "/api/v1/auth/me", Tags: []string{"Auth"},
		Summary: "The signed-in user", Security: sessionAuth,
	}, func(ctx context.Context, _ *struct{}) (*meOutput, error) {
		u, err := s.Store.Q.GetUser(ctx, viewer(ctx))
		if err != nil {
			return nil, err
		}
		return &meOutput{Body: s.userOf(u)}, nil
	})
}

// cookie is the session cookie. From the internet (through Funnel, always HTTPS) it's sent only
// over HTTPS, whatever PHOTOSTEAD_SECURE_COOKIES says.
func (s *server) cookie(ctx context.Context, value string, ttl time.Duration) http.Cookie {
	c := http.Cookie{Name: cookieName, Value: value, Path: "/", HttpOnly: true, Secure: s.SecureCookies || fromInternet(ctx), SameSite: http.SameSiteLaxMode}
	if ttl < 0 {
		c.MaxAge = -1
	} else {
		c.MaxAge = int(ttl.Seconds())
	}
	return c
}
