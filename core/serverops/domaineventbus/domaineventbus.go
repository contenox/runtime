package domaineventbus

// import (
// 	"context"
// 	"encoding/json"
// 	"strings"

// 	"github.com/js402/cate/core/serverops"
// 	"github.com/js402/cate/libs/libbus"
// )

// type domainEventBus struct {
// 	pubsub           libbus.Messenger
// 	subjectTemplates []string
// }

// type DomainEvent struct {
// 	Operation string `json:"operation"` // e.g. "FileService.UpdateFile"
// 	Subject   string `json:"subject"`   // e.g. "UpdateFile.file123"
// 	Payload   any    `json:"payload"`   // args or domain-specific content
// 	Err       string `json:"error,omitempty"`
// }

// func New(pubsub libbus.Messenger, subjectTemplates ...string) *domainEventBus {
// 	return &domainEventBus{
// 		pubsub:           pubsub,
// 		subjectTemplates: subjectTemplates,
// 	}
// }

// // Start implements serverops.ActivityTracker.
// func (d *domainEventBus) Start(operation string, args ...any) (func(error), func(string), func()) {
// 	var reportedErr error
// 	var reportedChange string

// 	// Report an error after the operation (if any)
// 	reportErr := func(err error) {
// 		reportedErr = err
// 	}

// 	// Report a semantic change (usually an ID or resource path)
// 	reportChange := func(subject string) {
// 		reportedChange = subject
// 	}

// 	end := func() {
// 		subject := d.resolveSubject(operation, reportedChange)
// 		if subject == "" {
// 			// no change, no publish
// 			return
// 		}

// 		evt := DomainEvent{
// 			Operation: operation,
// 			Subject:   reportedChange,
// 			Payload:   args,
// 		}
// 		if reportedErr != nil {
// 			evt.Err = reportedErr.Error()
// 		}

// 		data, err := json.Marshal(evt)
// 		if err != nil {
// 			// optionally log or swallow
// 			return
// 		}

// 		_ = d.pubsub.Publish(context.Background(), subject, data)
// 	}

// 	return reportErr, reportChange, end
// }

// func (d *domainEventBus) resolveSubject(ctx context.Context, operation string, reportedChange string) string {
// 	// Example: "files.changed.file123" or just "file.changed"
// 	if reportedChange == "" {
// 		return ""
// 	}
// 	return "domain." + sanitize(operation) + "." + reportedChange
// }

// func sanitize(op string) string {
// 	// Optional: normalize op names like "FileService.UpdateFile" → "fileservice.updatefile"
// 	return strings.ToLower(strings.ReplaceAll(op, ".", "_"))
// }

// var _ serverops.ActivityTracker = (*domainEventBus)(nil)
