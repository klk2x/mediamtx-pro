package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/twitchtv/twirp"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
)

const (
	authorizationHeader = "Authorization"
	bearerPrefix        = "Bearer "
	accessTokenParam    = "access_token"
)

type grantsKey struct{}

var (
	ErrPermissionDenied          = errors.New("permissions denied")
	ErrMissingAuthorization      = errors.New("invalid authorization header. Must start with " + bearerPrefix)
	ErrInvalidAuthorizationToken = errors.New("invalid authorization token")
)

// authentication middleware
type APIKeyAuthMiddleware struct {
	provider auth.KeyProvider
}

func NewAPIKeyAuthMiddleware(provider auth.KeyProvider) *APIKeyAuthMiddleware {
	return &APIKeyAuthMiddleware{
		provider: provider,
	}
}

func (m *APIKeyAuthMiddleware) AuthMiddleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// ctx.String(http.StatusForbidden, "无权限")
		// ctx.Abort()
		authHeader := ctx.Request.Header.Get(authorizationHeader)
		var authToken string

		// fmt.Println("AuthMiddleware 1 ==:", authHeader)

		if authHeader != "" {
			if !strings.HasPrefix(authHeader, bearerPrefix) {
				ctx.String(http.StatusUnauthorized, ErrMissingAuthorization.Error())
				ctx.Abort()
				return
			}

			authToken = authHeader[len(bearerPrefix):]
		} else {
			// attempt to find from request header
			authToken = ctx.Request.FormValue(accessTokenParam)
		}

		if authToken != "" {
			v, err := auth.ParseAPIToken(authToken)

			if err != nil {
				ctx.String(http.StatusUnauthorized, ErrInvalidAuthorizationToken.Error())
				ctx.Abort()
				return
			}
			secret := m.provider.GetSecret(v.APIKey())

			if secret == "" {
				ctx.String(http.StatusUnauthorized, "invalid API key")
				ctx.Abort()
				return
			}

			grants, err := v.Verify(secret)
			if err != nil {
				ctx.String(http.StatusUnauthorized, "invalid token:, error: "+err.Error())
				ctx.Abort()
				return
			}
			if grants != nil {
				ctx.Next()
				return
			}

			// str := fmt.Sprintf("%+v", grants)
			// fmt.Println("AuthMiddleware:", grants)
			// fmt.Println("AuthMiddleware:", str)
		}

		ctx.String(http.StatusUnauthorized, ErrPermissionDenied.Error())
		ctx.Abort()
	}
}
func GetGrants(ctx context.Context) *auth.ClaimGrants {
	val := ctx.Value(grantsKey{})
	claims, ok := val.(*auth.ClaimGrants)
	if !ok {
		return nil
	}
	return claims
}

func WithGrants(ctx context.Context, grants *auth.ClaimGrants) context.Context {
	return context.WithValue(ctx, grantsKey{}, grants)
}

func SetAuthorizationToken(r *http.Request, token string) {
	r.Header.Set(authorizationHeader, bearerPrefix+token)
}

func EnsureJoinPermission(ctx context.Context) (name livekit.RoomName, err error) {
	claims := GetGrants(ctx)
	if claims == nil || claims.Video == nil {
		err = ErrPermissionDenied
		return
	}

	if claims.Video.RoomJoin {
		name = livekit.RoomName(claims.Video.Room)
	} else {
		err = ErrPermissionDenied
	}
	return
}

func EnsureAdminPermission(ctx context.Context, room livekit.RoomName) error {
	claims := GetGrants(ctx)
	if claims == nil || claims.Video == nil {
		return ErrPermissionDenied
	}

	if !claims.Video.RoomAdmin || room != livekit.RoomName(claims.Video.Room) {
		return ErrPermissionDenied
	}

	return nil
}

func EnsureCreatePermission(ctx context.Context) error {
	claims := GetGrants(ctx)
	if claims == nil || claims.Video == nil || !claims.Video.RoomCreate {
		return ErrPermissionDenied
	}
	return nil
}

func EnsureListPermission(ctx context.Context) error {
	claims := GetGrants(ctx)
	if claims == nil || claims.Video == nil || !claims.Video.RoomList {
		return ErrPermissionDenied
	}
	return nil
}

func EnsureRecordPermission(ctx context.Context) error {
	claims := GetGrants(ctx)
	if claims == nil || claims.Video == nil || !claims.Video.RoomRecord {
		return ErrPermissionDenied
	}
	return nil
}

func EnsureIngressAdminPermission(ctx context.Context) error {
	claims := GetGrants(ctx)
	if claims == nil || claims.Video == nil || !claims.Video.IngressAdmin {
		return ErrPermissionDenied
	}
	return nil
}

// wraps authentication errors around Twirp
func twirpAuthError(err error) error {
	return twirp.NewError(twirp.Unauthenticated, err.Error())
}
