package openapi

import "context"

func (Server) DeleteBranch(ctx context.Context, request DeleteBranchRequestObject) (DeleteBranchResponseObject, error) {
	return DeleteBranch204Response{}, nil
}
