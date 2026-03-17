// Package report handles surfacing findings to operators via Kubernetes Events
// and (optionally) resource annotations.
package report

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/domain"
)

// Reporter surfaces findings as Kubernetes Events and/or resource annotations.
type Reporter interface {
	Report(ctx context.Context, obj client.Object, finding domain.Finding, decision domain.ActionDecision) error
}

// EventReason is the Kubernetes Event reason code for kube-janitor events.
type EventReason string

const (
	ReasonClassified    EventReason = "Classified"
	ReasonDryRunDelete  EventReason = "DryRunDelete"
	ReasonDeleted       EventReason = "Deleted"
	ReasonProtected     EventReason = "Protected"
	ReasonBlocked       EventReason = "Blocked"
	ReasonTTLExpired    EventReason = "TTLExpired"
	ReasonOrphaned      EventReason = "Orphaned"
	ReasonAbandoned     EventReason = "Abandoned"
	ReasonPRClosed      EventReason = "PRNamespaceClosed"
	ReasonError         EventReason = "Error"
)

// EventReporter emits Kubernetes Events for findings.
type EventReporter struct {
	recorder    record.EventRecorder
	scheme      *runtime.Scheme
	log         logr.Logger
	annotations bool
}

// NewEventReporter creates an EventReporter.
func NewEventReporter(recorder record.EventRecorder, scheme *runtime.Scheme, log logr.Logger, emitAnnotations bool) *EventReporter {
	return &EventReporter{
		recorder:    recorder,
		scheme:      scheme,
		log:         log,
		annotations: emitAnnotations,
	}
}

// Report emits a Kubernetes Event for the given finding and decision.
func (r *EventReporter) Report(
	ctx context.Context,
	obj client.Object,
	finding domain.Finding,
	decision domain.ActionDecision,
) error {
	eventType := corev1.EventTypeNormal
	reason, message := r.buildEvent(finding, decision)

	r.log.V(1).Info("emitting event",
		"object", finding.ObjectRef.String(),
		"reason", reason,
		"message", message,
	)

	r.recorder.Event(obj, eventType, string(reason), message)
	return nil
}

func (r *EventReporter) buildEvent(finding domain.Finding, decision domain.ActionDecision) (EventReason, string) {
	switch {
	case finding.Class == domain.ClassProtected:
		return ReasonProtected, fmt.Sprintf("Resource is protected: %s", summaryReasons(finding))

	case finding.Class == domain.ClassBlocked:
		return ReasonBlocked, fmt.Sprintf("Resource has blocking finalizers, cannot delete: %s", summaryReasons(finding))

	case finding.Class == domain.ClassExpired && decision.Action == domain.ActionDelete && decision.DryRun:
		return ReasonDryRunDelete, fmt.Sprintf("[DRY-RUN] Would delete expired resource. %s. Policy: %s",
			summaryReasons(finding), decision.PolicyRef)

	case finding.Class == domain.ClassExpired && decision.Action == domain.ActionDelete:
		return ReasonDeleted, fmt.Sprintf("Deleted expired resource. %s. Policy: %s",
			summaryReasons(finding), decision.PolicyRef)

	case finding.Class == domain.ClassExpired:
		return ReasonTTLExpired, fmt.Sprintf("Resource TTL has expired: %s", summaryReasons(finding))

	case finding.Class == domain.ClassOrphaned:
		return ReasonOrphaned, fmt.Sprintf("Resource is structurally orphaned (dead ownerRef): %s", summaryReasons(finding))

	case finding.Class == domain.ClassAbandoned:
		return ReasonAbandoned, fmt.Sprintf("Resource appears abandoned (no live references found): %s", summaryReasons(finding))

	default:
		return ReasonClassified, fmt.Sprintf("Resource classified as %s (confidence=%.2f): %s",
			finding.Class, finding.Confidence, summaryReasons(finding))
	}
}

// summaryReasons concatenates the top reason messages for a finding.
func summaryReasons(finding domain.Finding) string {
	if len(finding.Reasons) == 0 {
		return "no reasons recorded"
	}
	msg := finding.Reasons[0].Message
	if len(finding.Reasons) > 1 {
		msg = fmt.Sprintf("%s (+%d more)", msg, len(finding.Reasons)-1)
	}
	return msg
}
