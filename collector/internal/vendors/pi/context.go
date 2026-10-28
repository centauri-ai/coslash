package pi

import "context"

type sourcePlanKey struct{}

// WithSourcePlan shares one pass's planning/runtime snapshot with discovery.
func WithSourcePlan(ctx context.Context, plan *SourcePlan) context.Context {
	if plan != nil {
		ctx = WithRuntimeSnapshot(ctx, plan.runtime)
	}
	return context.WithValue(ctx, sourcePlanKey{}, plan)
}

func SourcePlanFromContext(ctx context.Context) (*SourcePlan, bool) {
	plan, ok := ctx.Value(sourcePlanKey{}).(*SourcePlan)
	return plan, ok
}
