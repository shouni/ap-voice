// Package domain defines ap-voice's framework-independent domain types and interfaces.
package domain

import "context"

// Notifier is the interface for sending notifications about generated content or errors to a target or channel.
//
// There are only two outcomes, success and failure. A sibling app has a third, "skipped because
// there is no diff", but an ap-voice job either produces a script or audio, or fails;
// there is no branch that corresponds to a skip.
type Notifier interface {
	// Notify sends the metadata of a successful run to the target.
	Notify(ctx context.Context, req Request, publicURL string) error
	// NotifyFailure notifies the target of the error from a failed run.
	NotifyFailure(ctx context.Context, req Request, err error) error
}
