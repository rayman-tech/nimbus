package database

import "context"

type userKeyType struct{}

var userKey userKeyType

func UserWithContext(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, userKey, user)
}

func UserFromContext(ctx context.Context) *User {
	user, ok := ctx.Value(userKey).(*User)
	if !ok {
		return nil
	}
	return user
}
