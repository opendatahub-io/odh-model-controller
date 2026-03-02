package middleware

import "context"

// ContextWithToken returns a context with the given token set.
// Exported for use by handler tests that bypass the Auth middleware.
func ContextWithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey, token)
}
