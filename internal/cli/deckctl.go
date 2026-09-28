package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"patchbay/internal/client"
	"patchbay/internal/config"
	"patchbay/internal/evidence"
	"patchbay/internal/jsonstrict"
	"patchbay/internal/version"
	"patchbay/pkg/protocol"
	"strconv"
	"strings"
	"time"
)

const pollInterval = 200 * time.Millisecond
const cancelBudget = 2 * time.Second

type commandError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	JobID   string `json:"-"`
	Exit    int    `json:"-"`
}

func (e *commandError) Error() string { return e.Message }

type validationResult struct {
	Valid bool `json:"valid"`
}

func runDeckctl(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	options, command, err := parseOptions(args)
	if err != nil {
		return report(nil, &commandError{Code: "usage", Message: err.Error(), Exit: 2}, options, stdout, stderr)
	}
	if options.help {
		if _, err := io.WriteString(stdout, ctlHelp); err != nil {
			return 1
		}
		return 0
	}
	if err := options.validate(command); err != nil {
		return report(nil, &commandError{Code: "usage", Message: err.Error(), Exit: 2}, options, stdout, stderr)
	}
	if options.version {
		if options.json {
			return writeJSON(stdout, version.Current())
		}
		if _, err := fmt.Fprintf(stdout, "deckctl %s\n", version.Current()); err != nil {
			return 1
		}
		return 0
	}
	if command[0] == "config" && command[1] == "validate" {
		if _, err := config.Load(options.config); err != nil {
			return report(nil, &protocol.Error{Code: protocol.InvalidConfig, Message: err.Error()}, options, stdout, stderr)
		}
		return report(validationResult{true}, nil, options, stdout, stderr)
	}
	if command[0] == "demo" {
		return runDemo(ctx, options.demoDir, stdout, stderr)
	}
	if command[0] == "workbench" {
		return runWorkbench(ctx, options.socket, stdout, stderr)
	}
	connection, err := client.New(client.Options{Socket: options.socket, Timeout: options.requestTimeout, MaxResponseBytes: options.maxResponse})
	if err != nil {
		return report(nil, err, options, stdout, stderr)
	}
	defer connection.Close()
	value, err := executeCommand(ctx, connection, options, command)
	return report(value, err, options, stdout, stderr)
}

func call[T any](ctx context.Context, c *client.Client, method string, request any, path ...string) (T, error) {
	var value T
	err := c.Call(ctx, method, path, request, &value)
	return value, err
}
func executeCommand(ctx context.Context, c *client.Client, o ctlOptions, command []string) (any, error) {
	if command[0] == "request-id" {
		return map[string]string{"request_id": evidence.NewRequestID(time.Now())}, nil
	}
	if command[0] == "capabilities" {
		return call[protocol.Capabilities](ctx, c, "GET", nil, "capabilities")
	}
	if command[0] == "status" {
		return call[protocol.Status](ctx, c, "GET", nil, "status")
	}
	switch command[0] + " " + command[1] {
	case "export prepare":
		return call[protocol.ExportPreview](ctx, c, "POST", protocol.ExportPrepare{Runs: []string{command[2], command[3]}}, "exports", "prepare")
	case "export save":
		return exportFile(ctx, c, command)
	case "experiment run":
		preview, err := call[protocol.CapturePreview](ctx, c, "POST", protocol.CapturePrepare{Experiment: command[2]}, "captures", "prepare")
		if err != nil {
			return nil, err
		}
		req := protocol.CaptureRequest{Preparation: preview.ID, Digest: preview.Digest, RequestID: evidence.NewRequestID(time.Now()), Confirmed: o.confirm}
		response, err := call[protocol.CaptureResponse](ctx, c, "POST", req, "captures")
		if err != nil {
			var known *protocol.Error
			if !errors.As(err, &known) {
				failure := classify(err)
				confirmation := ""
				if req.Confirmed {
					confirmation = " --confirm"
				}
				failure.Message += fmt.Sprintf(" Admission outcome is unknown. Retry this exact request: deckctl experiment capture %s %s %s%s", req.Preparation, req.Digest, req.RequestID, confirmation)
				return nil, failure
			}
			return nil, err
		}
		if o.async {
			return response, nil
		}
		for {
			saved, err := call[protocol.Run](ctx, c, "GET", nil, "runs", response.RunID)
			if err != nil {
				return nil, err
			}
			if evidence.Terminal(saved.State) {
				if saved.Error != nil {
					return saved, saved.Error
				}
				return saved, nil
			}
			select {
			case <-ctx.Done():
				cancelCtx, cancel := context.WithTimeout(context.Background(), cancelBudget)
				defer cancel()
				_, _ = call[protocol.Job](cancelCtx, c, "DELETE", nil, "jobs", response.JobID)
				return nil, ctx.Err()
			case <-time.After(pollInterval):
			}
		}
	case "experiment capture":
		return call[protocol.CaptureResponse](ctx, c, "POST", protocol.CaptureRequest{Preparation: command[2], Digest: command[3], RequestID: command[4], Confirmed: o.confirm}, "captures")
	case "run compare":
		return call[protocol.Comparison](ctx, c, "POST", protocol.ComparisonRequest{Baseline: resultReference(command[2]), Candidate: resultReference(command[3])}, "comparisons")
	case "sample list":
		return call[protocol.SampleList](ctx, c, "GET", nil, "samples")
	case "experiment list":
		return call[protocol.ExperimentList](ctx, c, "GET", nil, "experiments")
	case "experiment prepare":
		return call[protocol.CapturePreview](ctx, c, "POST", protocol.CapturePrepare{Experiment: command[2]}, "captures", "prepare")
	case "storage status":
		return call[protocol.StoreStatus](ctx, c, "GET", nil, "storage")
	case "run list", "run page":
		var value protocol.RunList
		query := url.Values{}
		if len(command) == 3 {
			query.Set("cursor", command[2])
		}
		err := c.CallQuery(ctx, "GET", []string{"runs"}, query, nil, &value)
		return value, err
	case "run show":
		return call[protocol.Run](ctx, c, "GET", nil, "runs", command[2])
	case "run annotate":
		var update protocol.AnnotationUpdate
		if err := jsonstrict.Decode([]byte(command[3]), &update); err != nil {
			return nil, usage("Annotation requires revision, title, note and pinned JSON fields.")
		}
		return call[protocol.Annotation](ctx, c, "PUT", update, "runs", command[2], "annotation")
	case "run delete":
		var value protocol.Deletion
		err := c.CallQuery(ctx, "DELETE", []string{"runs", command[2]}, url.Values{"acknowledge": {strconv.FormatBool(o.confirm)}}, nil, &value)
		return value, err
	case "baseline show", "baseline set":
		context, err := call[protocol.Context](ctx, c, "GET", nil, "context")
		if err != nil {
			return nil, err
		}
		var value protocol.Baseline
		method := "GET"
		var body any
		if command[1] == "set" {
			var update protocol.BaselineUpdate
			if err := jsonstrict.Decode([]byte(command[3]), &update); err != nil {
				return nil, usage("Baseline requires run_id and revision JSON fields.")
			}
			method = "PUT"
			body = update
		}
		err = c.CallQuery(ctx, method, []string{"baselines", command[2]}, url.Values{"project": {context.Project}}, body, &value)
		return value, err
	case "project list":
		return call[protocol.ProjectList](ctx, c, "GET", nil, "projects")
	case "project current":
		value, err := call[protocol.Context](ctx, c, "GET", nil, "context")
		if err != nil {
			return nil, err
		}
		return protocol.ProjectSelection{Project: value.Project}, nil
	case "project use":
		return call[protocol.Context](ctx, c, "PUT", protocol.ProjectSelection{Project: command[2]}, "context", "project")
	case "context show":
		return call[protocol.Context](ctx, c, "GET", nil, "context")
	case "context set":
		patch := protocol.ContextPatch{}
		switch command[2] {
		case "project":
			return call[protocol.Context](ctx, c, "PUT", protocol.ProjectSelection{Project: command[3]}, "context", "project")
		case "mode":
			patch.Mode = &command[3]
		case "values":
			var values map[string]string
			if err := jsonstrict.Decode([]byte(command[3]), &values); err != nil {
				return nil, usage("Context values must be a JSON object of strings without duplicate keys or nulls.")
			}
			patch.Values = &values
		default:
			return nil, usage("Context keys are project, mode, or values.")
		}
		return call[protocol.Context](ctx, c, "PATCH", patch, "context")
	case "action list":
		return call[protocol.ActionList](ctx, c, "GET", nil, "actions")
	case "workflow list":
		return call[protocol.WorkflowList](ctx, c, "GET", nil, "workflows")
	case "action run", "workflow run":
		return runInvocation(ctx, c, o, command[0], command[2])
	case "job list":
		return call[protocol.JobList](ctx, c, "GET", nil, "jobs")
	case "job show":
		return call[protocol.Job](ctx, c, "GET", nil, "jobs", command[2])
	case "job cancel":
		return call[protocol.Job](ctx, c, "DELETE", nil, "jobs", command[2])
	case "param list":
		return call[protocol.ParameterList](ctx, c, "GET", nil, "parameters")
	case "param get":
		return call[protocol.Parameter](ctx, c, "GET", nil, "parameters", command[2])
	case "param set":
		metadata, err := call[protocol.Parameter](ctx, c, "GET", nil, "parameters", command[2])
		if err != nil {
			return nil, err
		}
		value, err := parseScalar(metadata.Type, command[3])
		if err != nil {
			return nil, err
		}
		return call[protocol.Parameter](ctx, c, "PUT", protocol.ParameterSet{Value: value}, "parameters", command[2])
	case "config reload":
		return call[protocol.ReloadResponse](ctx, c, "POST", struct{}{}, "config", "reload")
	}
	return nil, usage("Unknown command.")
}

func parseScalar(kind, text string) (any, error) {
	switch kind {
	case "string", "enum":
		return text, nil
	case "integer":
		n, err := strconv.ParseInt(text, 10, 64)
		if err == nil {
			return n, nil
		}
		return nil, usage("Expected a signed 64-bit decimal integer.")
	case "float":
		n, err := strconv.ParseFloat(text, 64)
		if err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n, nil
		}
		return nil, usage("Expected a finite floating-point number.")
	case "boolean":
		if text == "true" {
			return true, nil
		}
		if text == "false" {
			return false, nil
		}
		return nil, usage("Expected true or false.")
	default:
		return nil, &client.Error{Code: "invalid_response", Message: "Daemon returned an unsupported value type."}
	}
}
func usage(message string) error { return &commandError{Code: "usage", Message: message, Exit: 2} }

func runInvocation(ctx context.Context, c *client.Client, o ctlOptions, kind, name string) (any, error) {
	invocation := protocol.Invocation{Mode: protocol.Async, Confirmed: o.confirm}
	if o.timeout > 0 {
		invocation.TimeoutMS = int64(o.timeout / time.Millisecond)
		if o.timeout%time.Millisecond != 0 {
			invocation.TimeoutMS++
		}
	}
	if len(o.args) > 0 {
		definition, err := call[protocol.Action](ctx, c, "GET", nil, "actions", name)
		if err != nil {
			return nil, err
		}
		if definition.Name != name {
			return nil, &client.Error{Code: "invalid_response", Message: "Daemon returned metadata for a different action."}
		}
		invocation.Args = map[string]any{}
		for key, text := range o.args {
			input, ok := definition.Inputs[key]
			if !ok {
				return nil, usage("Action argument is not declared in its schema.")
			}
			value, err := parseScalar(input.Type, text)
			if err != nil {
				return nil, err
			}
			invocation.Args[key] = value
		}
	}
	admitted, err := call[protocol.InvocationResponse](ctx, c, "POST", invocation, kind+"s", name)
	if err != nil {
		failure := classify(err)
		var known *protocol.Error
		if !errors.As(err, &known) {
			failure.Message += " Admission outcome is unknown; inspect job list before retrying."
		}
		return nil, failure
	}
	if !config.ValidName(admitted.JobID) {
		return nil, &client.Error{Code: "invalid_response", Message: "Daemon did not return a valid job ID. Inspect job list before retrying."}
	}
	if o.async {
		return admitted, nil
	}
	return waitForJob(ctx, c, admitted.JobID)
}

func waitForJob(ctx context.Context, c *client.Client, id string) (any, error) {
	for {
		if ctx.Err() != nil {
			return interruptJob(c, id)
		}
		value, err := call[protocol.Job](ctx, c, "GET", nil, "jobs", id)
		if ctx.Err() != nil {
			return interruptJob(c, id)
		}
		if err != nil {
			failure := classify(err)
			failure.JobID = id
			failure.Message += " Job may still be running; inspect job show."
			return nil, failure
		}
		if value.ID != id {
			return nil, &commandError{Code: "invalid_response", Message: "Daemon returned a different job ID.", JobID: id, Exit: 3}
		}
		switch value.State {
		case "success":
			return value, nil
		case "failed", "cancelled":
			if value.Error == nil {
				code := protocol.ExecutionFailed
				if value.State == "cancelled" {
					code = protocol.Cancelled
				}
				value.Error = &protocol.Error{Code: code, Message: "Job " + value.State + "."}
			}
			return value, value.Error
		case "queued", "running":
		default:
			return nil, &commandError{Code: "invalid_response", Message: "Daemon returned an unknown job state.", JobID: id, Exit: 3}
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return interruptJob(c, id)
		case <-timer.C:
		}
	}
}
func interruptJob(c *client.Client, id string) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cancelBudget)
	defer cancel()
	value, err := call[protocol.Job](ctx, c, "DELETE", nil, "jobs", id)
	message := "Cancellation requested; inspect job show for its final outcome."
	if err != nil || value.ID != id {
		message = "Cancellation could not be acknowledged; the job may still be running. Inspect job show."
	} else if value.State == "success" || value.State == "failed" || value.State == "cancelled" {
		message = "Interrupted; the job is already " + value.State + "."
	}
	return nil, &commandError{Code: string(protocol.Cancelled), Message: message, JobID: id, Exit: 130}
}

func classify(err error) *commandError {
	var own *commandError
	if errors.As(err, &own) {
		copy := *own
		return &copy
	}
	if errors.Is(err, context.Canceled) {
		return &commandError{Code: "cancelled", Message: "Request was interrupted.", Exit: 130}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &commandError{Code: "timeout", Message: "HTTP request timed out; adjust --request-timeout if needed.", Exit: 124}
	}
	var transport *client.Error
	if errors.As(err, &transport) {
		code := 3
		if transport.Code == "usage" {
			code = 2
		}
		return &commandError{Code: transport.Code, Message: transport.Message, Exit: code}
	}
	var apiError *protocol.Error
	if errors.As(err, &apiError) {
		exit := 1
		switch apiError.Code {
		case protocol.InvalidRequest, protocol.NotFound, protocol.ActionNotFound, protocol.ProjectNotFound:
			exit = 2
		case protocol.ProviderUnavailable, protocol.Busy, protocol.ShuttingDown:
			exit = 3
		case protocol.PermissionDenied, protocol.ConfirmationRequired:
			exit = 4
		case protocol.ExecutionFailed:
			exit = 5
		case protocol.Timeout:
			exit = 124
		case protocol.Cancelled:
			exit = 130
		}
		return &commandError{Code: string(apiError.Code), Message: apiError.Message, Exit: exit}
	}
	return &commandError{Code: "internal", Message: "Command failed.", Exit: 1}
}

func report(value any, err error, o ctlOptions, stdout, stderr io.Writer) int {
	if err != nil {
		failure := classify(err)
		if job, ok := value.(protocol.Job); !ok || job.ID == "" {
			value = nil
		}
		if o.json {
			if value == nil {
				value = struct {
					Error *commandError `json:"error"`
					JobID string        `json:"job_id,omitempty"`
				}{failure, failure.JobID}
			}
			if code := writeJSON(stdout, value); code != 0 {
				return code
			}
		} else if value != nil {
			if _, e := io.WriteString(stdout, render(value)); e != nil {
				return 1
			}
		}
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", failure.Code, failure.Message)
		if failure.JobID != "" {
			_, _ = fmt.Fprintf(stderr, "Job ID: %s\n", failure.JobID)
		}
		if failure.Code == string(protocol.ConfirmationRequired) {
			_, _ = fmt.Fprintln(stderr, "Review the action, then retry the same command with --confirm.")
		}
		return failure.Exit
	}
	if o.json {
		return writeJSON(stdout, value)
	}
	if _, err := io.WriteString(stdout, render(value)); err != nil {
		return 1
	}
	return 0
}

func resultReference(value string) protocol.ResultReference {
	if id, ok := strings.CutPrefix(value, "sample:"); ok {
		return protocol.ResultReference{Kind: "sample", ID: id}
	}
	return protocol.ResultReference{Kind: "run", ID: value}
}
