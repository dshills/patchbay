package runtime

import (
	"context"
	"patchbay/internal/evidence"
	"patchbay/internal/fault"
	"patchbay/internal/jsonstrict"
	"patchbay/pkg/protocol"
)

func (r *Runtime) Compare(ctx context.Context, request protocol.ComparisonRequest) (protocol.Comparison, error) {
	if err := ctx.Err(); err != nil {
		return protocol.Comparison{}, fault.Safe(err)
	}
	store, err := r.Evidence()
	if err != nil {
		return protocol.Comparison{}, err
	}
	ids := []string{}
	for _, ref := range []protocol.ResultReference{request.Baseline, request.Candidate} {
		if ref.Kind == "run" {
			ids = append(ids, ref.ID)
		} else if ref.Kind != "sample" && ref.Kind != "recipe_sample" {
			return protocol.Comparison{}, fault.New(protocol.InvalidRequest, "Result references require kind run, sample or recipe_sample.")
		}
	}
	release, err := store.Hold(ids...)
	if err != nil {
		return protocol.Comparison{}, err
	}
	defer release()
	resolve := func(ref protocol.ResultReference) (protocol.Run, map[string]protocol.Series, error) {
		values := map[string]protocol.Series{}
		if ref.Kind == "recipe_sample" {
			sample, err := r.RecipeSample(ctx, ref)
			if err != nil {
				return protocol.Run{}, nil, err
			}
			for _, series := range sample.Series {
				values[series.Name] = series
			}
			return evidence.SampleRun(sample), values, nil
		}
		if ref.Kind == "sample" {
			for _, sample := range evidence.Samples().Samples {
				if sample.ID == ref.ID {
					for _, series := range sample.Series {
						values[series.Name] = series
					}
					return evidence.SampleRun(sample), values, nil
				}
			}
			return protocol.Run{}, nil, fault.New(protocol.NotFound, "Sample not found.")
		}
		run, err := store.Get(ref.ID)
		if err != nil {
			return run, nil, err
		}
		for _, artifact := range run.Artifacts {
			if err := ctx.Err(); err != nil {
				return run, nil, fault.Safe(err)
			}
			if artifact.MediaType != "application/json" {
				continue
			}
			data, _, err := store.Artifact(run.ID, artifact.ID)
			if err != nil {
				return run, nil, err
			}
			var value protocol.Series
			if jsonstrict.Decode(data, &value) == nil {
				values[value.Name] = value
			}
		}
		return run, values, nil
	}
	left, a, err := resolve(request.Baseline)
	if err != nil {
		return protocol.Comparison{}, err
	}
	right, b, err := resolve(request.Candidate)
	if err != nil {
		return protocol.Comparison{}, err
	}
	comparison := evidence.Compare(left, right)
	if !comparison.Compatible {
		return comparison, nil
	}
	for _, collector := range right.Experiment.Collectors {
		if collector.Kind != "series" {
			continue
		}
		first, ok := a[collector.Name]
		second, exists := b[collector.Name]
		if !ok || !exists {
			comparison.Series = append(comparison.Series, protocol.SeriesDelta{Name: collector.Name, Reason: "A series is missing."})
			continue
		}
		comparison.Series = append(comparison.Series, evidence.CompareSeries(first, second))
	}
	return comparison, nil
}
