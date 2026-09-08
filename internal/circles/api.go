package circles

import (
	"net/http"
	"time"

	_ "time/tzdata"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irairdon/gritual/internal/auth"
	"github.com/irairdon/gritual/internal/config"
	"github.com/irairdon/gritual/internal/httpx"
)

const (
	defaultTZ  = "America/Denver"
	maxMembers = 50
	inviteTTL  = 14 * 24 * time.Hour
	roleOwner  = "owner"
	roleAdmin  = "admin"
	roleMember = "member"
)

type API struct {
	cfg  config.Config
	pool *pgxpool.Pool
}

func New(cfg config.Config, pool *pgxpool.Pool) *API {
	return &API{cfg: cfg, pool: pool}
}

func (a *API) Mount(r chi.Router) {
	r.Get("/circles", require(a.handleList))
	r.Post("/circles", require(a.handleCreate))
	r.Get("/circles/{id}", require(a.handleGet))
	r.Patch("/circles/{id}", require(a.handlePatch))
	r.Delete("/circles/{id}", require(a.handleDelete))
	r.Get("/circles/{id}/members", require(a.handleMembers))
	r.Delete("/circles/{id}/members/{userID}", require(a.handleRemoveMember))
	r.Post("/circles/{id}/transfer", require(a.handleTransfer))
	r.Post("/circles/{id}/invites", require(a.handleCreateInvite))
	r.Delete("/circles/{id}/invites/{inviteID}", require(a.handleRevokeInvite))
	r.Post("/invites/{token}/accept", require(a.handleAcceptInvite))
}

func require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auth.UserFrom(r.Context()) == nil {
			httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
			return
		}
		next(w, r)
	}
}

type circleOut struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Emoji       *string   `json:"emoji"`
	TZ          string    `json:"tz"`
	Role        string    `json:"role"`
	MemberCount int       `json:"member_count"`
}

type memberOut struct {
	UserID      uuid.UUID `json:"user_id"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	JoinedAt    time.Time `json:"joined_at"`
}

type inviteOut struct {
	ID        uuid.UUID `json:"id"`
	Token     string    `json:"token,omitempty"`
	URL       string    `json:"url,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	MaxUses   *int      `json:"max_uses"`
}

func canManage(role string) bool {
	return role == roleOwner || role == roleAdmin
}

func validTZ(tz string) bool {
	if tz == "" || tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}
