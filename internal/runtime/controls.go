package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"patchbay/internal/binding"
	"patchbay/internal/config"
	"patchbay/internal/event"
	"patchbay/internal/fault"
	"patchbay/internal/identity"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/permission"
	"patchbay/pkg/protocol"
)

var controlGestures = []string{event.ControlPressed, event.ControlReleased, event.ControlRotated, event.ControlLongPressed, event.ControlTouched, event.ControlLongTouched}

const confirmationTTL = 5 * time.Second
const maxConfirmations = 256

type controlConfirmation struct {
	source, device, control, gesture string
	guard                            protocol.ControlGuard
	expires                          time.Time
}

func (r *Runtime) controlGuard() protocol.ControlGuard {
	return protocol.ControlGuard{Instance: r.instance, Revision: r.controlRevision}
}

// Called only while holding r.mu, including away-and-back context changes.
func (r *Runtime) invalidateControls() {
	r.controlRevision++
	r.confirmations = nil
}

func (r *Runtime) ControlSnapshot(ctx context.Context, request protocol.ControlSnapshotRequest) (protocol.ControlSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writable(ctx); err != nil {
		return protocol.ControlSnapshot{}, err
	}
	if len(request.Controls) > 64 {
		return protocol.ControlSnapshot{}, fault.New(protocol.InvalidRequest, "At most 64 controls may be inspected.")
	}
	state := cloneContext(r.context)
	result := protocol.ControlSnapshot{Guard: r.controlGuard(), Generation: r.generation, Context: protocol.Context{Project: state.Project, Mode: state.Mode, Values: state.Values}, Controls: make([]protocol.ControlView, 0, len(request.Controls))}
	seen := map[protocol.ControlRef]bool{}
	for _, ref := range request.Controls {
		if !config.ValidName(ref.Control) || ref.Device != "" && !config.ValidName(ref.Device) || seen[ref] {
			return protocol.ControlSnapshot{}, fault.New(protocol.InvalidRequest, "Invalid or duplicate control reference.")
		}
		seen[ref] = true
		view := protocol.ControlView{ControlRef: ref, Targets: map[string]protocol.ControlTarget{}}
		for _, gesture := range controlGestures {
			target := binding.Resolve(r.cfg.Bindings, ref.Device, ref.Control, gesture, r.context)
			if target == nil {
				continue
			}
			wire := protocol.ControlTarget{Action: target.Action, Enabled: true}
			if target.Parameter != "" {
				p := r.parameters[target.Parameter]
				wire.Parameter = &protocol.Parameter{Name: target.Parameter, Type: string(p.Type), Value: p.Value, Min: p.Min, Max: p.Max, Step: p.Step, Enum: slices.Clone(p.Enum), Unit: p.Unit, Persistent: p.Persistent}
			} else {
				risk := r.actionRisk(target.Action, map[string]bool{})
				wire.Safety = string(risk)
				wire.Enabled = permission.Check(risk, r.cfg.Security.AllowDangerousActions, true) == nil
			}
			view.Targets[gesture] = wire
		}
		result.Controls = append(result.Controls, view)
	}
	return result, nil
}

type controlPayload struct {
	Device       string                 `json:"device"`
	Control      string                 `json:"control"`
	Delta        *int64                 `json:"delta,omitempty"`
	Confirmed    *bool                  `json:"confirmed,omitempty"`
	Guard        *protocol.ControlGuard `json:"guard,omitempty"`
	Confirmation string                 `json:"confirmation,omitempty"`
}

func (r *Runtime) Control(ctx context.Context, request protocol.EventRequest) (protocol.EventResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writable(ctx); err != nil {
		return protocol.EventResponse{}, err
	}
	if !slices.Contains(controlGestures, request.Type) {
		return protocol.EventResponse{}, fault.New(protocol.InvalidRequest, "Only external control events are accepted.")
	}
	var payload controlPayload
	rotation := request.Type == event.ControlRotated
	if err := jsonstrict.Decode(request.Payload, &payload); err != nil || !config.ValidName(payload.Control) || !config.ValidName(request.Source) || payload.Device != "" && !config.ValidName(payload.Device) || rotation != (payload.Delta != nil) || rotation && (payload.Confirmed != nil || payload.Confirmation != "") || payload.Guard != nil && payload.Confirmed != nil || payload.Confirmation != "" && payload.Guard == nil {
		return protocol.EventResponse{}, fault.New(protocol.InvalidRequest, "Invalid control payload.")
	}
	if payload.Guard != nil && *payload.Guard != r.controlGuard() {
		return protocol.EventResponse{}, fault.New(protocol.InvalidRequest, "Control snapshot is stale; refresh before new input.")
	}
	confirmed := payload.Confirmed != nil && *payload.Confirmed
	if payload.Confirmation != "" {
		ticket, ok := r.confirmations[payload.Confirmation]
		delete(r.confirmations, payload.Confirmation)
		if !ok || !time.Now().Before(ticket.expires) || ticket.guard != r.controlGuard() || ticket.source != request.Source || ticket.device != payload.Device || ticket.control != payload.Control || ticket.gesture != request.Type {
			return protocol.EventResponse{}, fault.New(protocol.InvalidRequest, "Confirmation expired or does not match this input.")
		}
		confirmed = true
	}
	target := binding.Resolve(r.cfg.Bindings, payload.Device, payload.Control, request.Type, r.context)
	response := protocol.EventResponse{EventID: identity.New(), Matched: target != nil}
	if target != nil {
		if target.Parameter != "" {
			if _, err := r.setParameter(target.Parameter, nil, payload.Delta); err != nil {
				return protocol.EventResponse{}, err
			}
		} else {
			handle, err := r.invoke(ctx, target.Action, false, protocol.Invocation{Mode: protocol.Async, Args: target.Args, Confirmed: confirmed})
			if err != nil {
				var known *protocol.Error
				if payload.Guard != nil && errors.As(err, &known) && known.Code == protocol.ConfirmationRequired {
					return protocol.EventResponse{}, r.challenge(request.Source, payload, request.Type, target.Action)
				}
				return protocol.EventResponse{}, err
			}
			response.JobID = handle.ID
		}
	}
	// Challenges are not retained in bus payloads or operation logs.
	payload.Confirmation = ""
	data, _ := json.Marshal(payload)
	r.bus.Publish(ctx, event.Event{ID: response.EventID, Type: request.Type, Source: request.Source, Timestamp: time.Now().UTC(), Payload: data})
	return response, nil
}

func (r *Runtime) challenge(source string, payload controlPayload, gesture, action string) error {
	now := time.Now()
	for token, ticket := range r.confirmations {
		if !now.Before(ticket.expires) || ticket.source == source && ticket.device == payload.Device && ticket.control == payload.Control {
			delete(r.confirmations, token)
		}
	}
	if len(r.confirmations) >= maxConfirmations {
		return fault.New(protocol.Busy, "Too many pending confirmations.")
	}
	if r.confirmations == nil {
		r.confirmations = map[string]controlConfirmation{}
	}
	token := identity.New()
	expires := now.Add(confirmationTTL)
	r.confirmations[token] = controlConfirmation{source: source, device: payload.Device, control: payload.Control, gesture: gesture, guard: r.controlGuard(), expires: expires}
	return &protocol.Error{Code: protocol.ConfirmationRequired, Message: "Hold this control to confirm within five seconds.", Confirmation: &protocol.ControlConfirmation{Token: token, Action: action, ExpiresAt: expires}}
}
