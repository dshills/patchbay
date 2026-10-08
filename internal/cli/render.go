package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"patchbay/internal/decksetup"
	"patchbay/internal/recipe"
	"patchbay/internal/supervisor"
	"patchbay/pkg/protocol"
	"slices"
	"strings"
	"time"
)

func render(value any) string {
	var out strings.Builder
	line := func(format string, args ...any) { _, _ = fmt.Fprintf(&out, format+"\n", args...) }
	switch v := value.(type) {
	case []decksetup.Device:
		for _, d := range v {
			line("%s\t%s", d.ID, d.Name)
		}
	case []decksetup.Preset:
		for _, p := range v {
			line("%s\t%s", p.Name, p.Description)
		}
	case []deckBackup:
		if len(v) == 0 {
			line("No deck backups yet. Run deckctl deck use demo to get started.")
		}
		for _, b := range v {
			line("%s\t%s\t%s", b.ID, b.Created, b.Reason)
		}
		if len(v) > 0 {
			line("Restore previous: deckctl deck restore\nRestore original: deckctl deck restore original\nRestore one: deckctl deck restore <backup-id>")
		}
	case decksetup.Result:
		if v.Restored != "" {
			line("Restored backup %s. Stream Deck reopened.", v.Restored)
		} else {
			line("Patchbay %s is ready on your Stream Deck+. Stream Deck reopened.", v.Preset)
		}
		line("Your previous setup was saved as %s.", v.Backup)
		line("Go back: deckctl deck restore\nReturn to your original setup: deckctl deck restore original")
	case supervisor.ProposalPreview:
		data, _ := json.MarshalIndent(v, "", "  ")
		line("%s", data)
		if v.Patch != nil {
			line("Complete diff:\n%s", v.Patch.Diff)
		}
	case protocol.PatchList, supervisor.GrantReview, supervisor.Admission, supervisor.Catalog, supervisor.ContextPreview, supervisor.Session, supervisor.List, recipe.ExportPreview, recipe.List, recipe.View, recipe.Preview, recipe.ManagementResult, recipe.ImportResult:
		data, _ := json.MarshalIndent(value, "", "  ")
		line("%s", data)
	case *recipe.Package:
		line("%s %s · declared ID %s", v.Manifest.Name, v.Manifest.Version, v.Manifest.ID)
		line("Package: %s\nLicense: %s · author: %s", v.Digest, v.Manifest.License, v.Manifest.Author)
		line("Offline inspection only. Local mappings and activation are required before execution.")
		for _, name := range slices.Sorted(maps.Keys(v.Manifest.Requirements)) {
			r := v.Manifest.Requirements[name]
			line("Role %s: %s · optional=%t · %s", name, r.Kind, r.Optional, r.Description)
		}
		data, _ := json.MarshalIndent(v.Manifest.Actions, "", "  ")
		line("Portable action definitions (local paths unresolved):\n%s", data)
		line("%d experiments · %d samples · %d verified payload files", len(v.Manifest.Experiments), len(v.Samples), len(v.Manifest.Inventory))
	case protocol.ExperimentList:
		for _, e := range v.Experiments {
			line("%s\t%s", e.ID, e.Title)
		}
		if len(v.Experiments) == 0 {
			line("No experiments configured.")
		}
	case protocol.StoreStatus:
		line("Run storage: available=%t read-only=%t; %d/%d runs; %d bytes saved, %d reserved", v.Available, v.ReadOnly, v.Runs, v.MaxRuns, v.Bytes, v.Reserved)
		for _, d := range v.Diagnostics {
			line("%s", d)
		}
	case protocol.RunList:
		for _, r := range v.Runs {
			line("%s\t%s\t%s\t%s", r.ID, r.Experiment, r.State, r.CreatedAt.Format(time.RFC3339))
		}
		if len(v.Runs) == 0 {
			line("No saved runs.")
		}
		if v.NextCursor != "" {
			line("Next page: deckctl run page %s", v.NextCursor)
		}
	case protocol.SampleList, protocol.ExportPreview, protocol.Comparison, protocol.CaptureResponse, map[string]string, protocol.CapturePreview, protocol.Capabilities, protocol.Run, protocol.Annotation, protocol.Baseline:
		data, _ := json.MarshalIndent(value, "", "  ")
		line("%s", data)

	case validationResult:
		line("Configuration is valid.")
	case protocol.Status:
		line("deckd %s · generation %d · uptime %s", v.Version, v.Generation, time.Duration(v.UptimeMS)*time.Millisecond)
		line("Config: %s", v.ConfigPath)
		line("Project: %s  Mode: %s  Running jobs: %d", orNone(v.Project), orNone(v.Mode), v.RunningJobs)
		for _, name := range slices.Sorted(maps.Keys(v.Providers)) {
			health := v.Providers[name]
			status := "available"
			if !health.Available {
				status = "unavailable (" + health.Code + ")"
			}
			line("Provider %s: %s", name, status)
		}
		for _, name := range slices.Sorted(maps.Keys(v.Devices)) {
			d := v.Devices[name]
			line("Instrument %s: %s %s at %s; shutdown=%s; %s", name, d.Profile, d.Model, d.Address, d.Shutdown, strings.Join(d.Capabilities, ", "))
		}
		for _, name := range slices.Sorted(maps.Keys(v.Plugins)) {
			p := v.Plugins[name]
			line("Plugin %s: protocol=%d discovered=%t operations=%s", name, p.Protocol, p.Discovered, strings.Join(slices.Sorted(maps.Keys(p.Operations)), ", "))
		}
	case protocol.Context:
		line("Project: %s", orNone(v.Project))
		line("Mode: %s", orNone(v.Mode))
		for _, key := range slices.Sorted(maps.Keys(v.Values)) {
			line("values.%s: %s", key, v.Values[key])
		}
	case protocol.ProjectSelection:
		line("%s", orNone(v.Project))
	case protocol.ProjectList:
		if len(v.Projects) == 0 {
			line("No projects.")
		}
		for _, p := range v.Projects {
			line("%s\t%s\t%s", p.ID, p.Name, p.Path)
		}
	case protocol.ActionList:
		if len(v.Actions) == 0 {
			line("No actions.")
		}
		for _, a := range v.Actions {
			line("%s\t%s\t%s", a.Name, a.Type, a.Safety)
			if p := a.Plugin; p != nil {
				line("  plugin: %s operation=%s protocol=%d", p.Name, p.Operation, p.Protocol)
			}
			if d := a.Instrument; d != nil {
				line("  instrument: %s channel=%d operation=%s parameter=%s unit=%s", d.Device, d.Channel, d.Operation, d.Parameter, d.Unit)
			}
			if a.Origin != "" {
				line("  origin: %s", a.Origin)
			}
			if a.Agent != nil {
				line("  %s: prompt=%s model=%s; network=%s; workspace_write=%t; tools=%s", a.Agent.Provider, a.Agent.Prompt, a.Agent.Model, a.Agent.Network, a.Agent.WorkspaceWrite, strings.Join(a.Agent.Tools, ","))
				if len(a.Agent.Files) > 0 {
					line("  context files: %s", strings.Join(a.Agent.Files, ", "))
				}
			}
			for _, key := range slices.Sorted(maps.Keys(a.Inputs)) {
				input := a.Inputs[key]
				requirement := "optional"
				if input.Required {
					requirement = "required"
				}
				suffix := ""
				if input.Default != nil {
					suffix = fmt.Sprintf("; default=%v", input.Default)
				}
				if len(input.Enum) > 0 {
					suffix += "; choices=" + strings.Join(input.Enum, ",")
				}
				line("  %s: %s (%s%s)", key, input.Type, requirement, suffix)
			}
		}
	case protocol.WorkflowList:
		if len(v.Workflows) == 0 {
			line("No workflows.")
		}
		for _, w := range v.Workflows {
			names := make([]string, 0, len(w.Steps))
			for _, step := range w.Steps {
				names = append(names, step.Action)
			}
			line("%s\tstop_on_error=%t\t%s", w.Name, w.StopOnError, strings.Join(names, " -> "))
		}
	case protocol.Parameter:
		renderParameter(&out, v)
	case protocol.ParameterList:
		if len(v.Parameters) == 0 {
			line("No parameters.")
		}
		for _, p := range v.Parameters {
			renderParameter(&out, p)
		}
	case protocol.JobList:
		if len(v.Jobs) == 0 {
			line("No jobs.")
		}
		for _, j := range v.Jobs {
			line("%s\t%s\t%s\t%s", j.ID, j.State, j.Action, j.CreatedAt.Format(time.RFC3339))
		}
	case protocol.Job:
		line("Job %s: %s (%s, generation %d)", v.ID, v.State, v.Action, v.Generation)
		if v.Result != nil {
			if v.Result.Message != "" {
				line("%s", v.Result.Message)
			}
			if d := v.Result.Display; d != nil && d.Progress != nil {
				line("Progress: %d%%", *d.Progress)
			}
			renderData(&out, v.Result.Data, "")
		}
		if v.Error != nil {
			line("%s: %s", v.Error.Code, v.Error.Message)
		}
	case protocol.InvocationResponse:
		line("%s", v.JobID)
	case protocol.Deletion:
		line("Run deleted: %t", v.Deleted)
	case protocol.ReloadResponse:
		line("Configuration reloaded (generation %d).", v.Generation)
	}
	return out.String()
}
func orNone(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}
func renderParameter(out *strings.Builder, p protocol.Parameter) {
	if s := p.Synchronization; s != nil {
		_, _ = fmt.Fprintf(out, "%s: desired=%v %s; observed=%v; synchronization=%s", p.Name, s.Desired, p.Unit, s.Observed, s.Status)
		if s.ObservedAt != nil {
			_, _ = fmt.Fprintf(out, "; observed_at=%s", s.ObservedAt.Format(time.RFC3339))
		}
		if s.ErrorCode != "" {
			_, _ = fmt.Fprintf(out, "; error=%s", s.ErrorCode)
		}
		out.WriteByte('\n')
		return
	}
	unit := ""
	if p.Unit != "" {
		unit = " " + p.Unit
	}
	_, _ = fmt.Fprintf(out, "%s = %v%s (%s; persistent=%t)", p.Name, p.Value, unit, p.Type, p.Persistent)
	if p.Min != nil {
		_, _ = fmt.Fprintf(out, " min=%v", p.Min)
	}
	if p.Max != nil {
		_, _ = fmt.Fprintf(out, " max=%v", p.Max)
	}
	if p.Step != nil {
		_, _ = fmt.Fprintf(out, " step=%v", p.Step)
	}
	if len(p.Enum) > 0 {
		_, _ = fmt.Fprintf(out, " choices=%s", strings.Join(p.Enum, ","))
	}
	out.WriteByte('\n')
}
func renderData(out *strings.Builder, data map[string]any, indent string) {
	if observed, ok := data["observed"].(map[string]any); ok {
		for _, name := range slices.Sorted(maps.Keys(observed)) {
			_, _ = fmt.Fprintf(out, "%s%s: observed=%v\n", indent, name, observed[name])
		}
	}
	if wave, ok := data["waveform"].(map[string]any); ok {
		_, _ = fmt.Fprintf(out, "%sWaveform: channel=%v points=%v x_increment=%v s (samples in JSON output)\n", indent, wave["channel"], wave["points"], wave["x_increment_s"])
	}
	structured := false
	if entries, ok := data["git_status"].([]any); ok {
		structured = true
		for _, item := range entries {
			entry, _ := item.(map[string]any)
			_, _ = fmt.Fprintf(out, "%s%v%v %q", indent, entry["index"], entry["worktree"], entry["path"])
			if original, ok := entry["original_path"]; ok {
				_, _ = fmt.Fprintf(out, " (from %q)", original)
			}
			out.WriteByte('\n')
		}
	}
	if commits, ok := data["git_log"].([]any); ok {
		structured = true
		for _, item := range commits {
			entry, _ := item.(map[string]any)
			_, _ = fmt.Fprintf(out, "%s%v %v\n", indent, entry["commit"], entry["subject"])
		}
	}
	for _, key := range []string{"stdout", "stderr"} {
		if key == "stdout" && structured {
			continue
		}
		if text, ok := data[key].(string); ok && text != "" {
			_, _ = fmt.Fprintf(out, "%s%s:\n%s", indent, key, text)
			if !strings.HasSuffix(text, "\n") {
				out.WriteByte('\n')
			}
		}
	}
	if truncated, _ := data["truncated"].(bool); truncated {
		_, _ = fmt.Fprintf(out, "%s[output truncated]\n", indent)
	}
	if incomplete, _ := data["structured_incomplete"].(bool); incomplete {
		_, _ = fmt.Fprintf(out, "%s[structured Git output incomplete]\n", indent)
	}
	steps, _ := data["steps"].([]any)
	for i, item := range steps {
		step, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := step["action"].(string)
		if skipped, _ := step["skipped"].(bool); skipped {
			_, _ = fmt.Fprintf(out, "%sStep %d %s: skipped\n", indent, i+1, name)
			continue
		}
		result, _ := step["result"].(map[string]any)
		status, _ := result["status"].(string)
		_, _ = fmt.Fprintf(out, "%sStep %d %s: %s\n", indent, i+1, name, status)
		nested, _ := result["data"].(map[string]any)
		renderData(out, nested, indent+"  ")
		if failure, ok := step["error"].(map[string]any); ok {
			_, _ = fmt.Fprintf(out, "%s  %v: %v\n", indent, failure["code"], failure["message"])
		}
	}
}
