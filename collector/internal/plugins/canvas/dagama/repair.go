package dagama

import "context"

func (c *Controller) openRepairGate(ctx context.Context, state *RunState, component ComponentID, instance int, message string) (*RunState, error) {
	var revision *uint64
	if state.Change != nil {
		value := state.Change.ChangeRevision
		revision = &value
	}
	return c.runs.Append(ctx, state.ProjectID, state.RunID, &GateOpened{
		ComponentInstance: ComponentInstance{ComponentID: component, Instance: instance},
		Reason:            "waiting_for_repair", Message: message, ChangeRevision: revision,
	})
}
