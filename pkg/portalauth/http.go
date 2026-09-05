package portalauth

import (
	"context"
	"errors"
	"net/http"
)

type ctxKey struct{}

// FromContext returns claims stored by AuthenticateMiddleware.
func FromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(*Claims)
	return c, ok && c != nil
}

func withClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, ctxKey{}, claims)
}

// WithClaims stores verified claims on a context. Used by HTTP adapters.
func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return withClaims(ctx, claims)
}

func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrMissingToken), errors.Is(err, ErrInvalidToken), errors.Is(err, ErrExpiredToken), errors.Is(err, ErrWrongUse):
		http.Error(w, err.Error(), http.StatusUnauthorized)
	default:
		http.Error(w, err.Error(), http.StatusForbidden)
	}
}

// AuthenticateMiddleware is @authenticate for net/http.
func AuthenticateMiddleware(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, err := AuthenticateRequest(secret, r)
			if err != nil {
				writeAuthError(w, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), claims)))
		})
	}
}

// AuthorizeMiddleware is @authorize for net/http. Run after AuthenticateMiddleware.
func AuthorizeMiddleware(rules ...Rule) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := FromContext(r.Context())
			if !ok {
				writeAuthError(w, ErrMissingToken)
				return
			}
			if err := Authorize(claims, rules...); err != nil {
				writeAuthError(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Protect is @authenticate + @authorize on a single handler.
func Protect(secret []byte, next http.Handler, rules ...Rule) http.Handler {
	h := next
	if len(rules) > 0 {
		h = AuthorizeMiddleware(rules...)(h)
	}
	return AuthenticateMiddleware(secret)(h)
}
