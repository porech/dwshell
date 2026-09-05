package manage

import (
	"context"
	"fmt"
)

// Agent is an agent record as the service echoes it back after a change.
type Agent struct {
	ID          string
	Name        string
	Description string
	// InstallCode is the code that binds an installation to this agent. The
	// service mints it on creation and clears it once the agent is installed,
	// so it is set only while State is "W".
	InstallCode int
	State       string
}

// agentFromItem reads the fields we care about out of an echoed record.
func agentFromItem(it item) *Agent {
	a := &Agent{}
	if s, ok := it["id"].(string); ok {
		a.ID = s
	}
	if s, ok := it["name"].(string); ok {
		a.Name = s
	}
	if s, ok := it["description"].(string); ok {
		a.Description = s
	}
	if s, ok := it["state"].(string); ok {
		a.State = s
	}
	// The code is a JSON number on the wire ("tempCode":281407902), which
	// through an untyped map lands as a float64.
	if f, ok := it["tempCode"].(float64); ok {
		a.InstallCode = int(f)
	}
	return a
}

// CreateAgent registers a new agent and returns it with its installation code.
// The service mints the code as part of creating the record, so this is one
// round trip rather than a create followed by a read. idGroup may be empty,
// which puts the agent in no group.
func CreateAgent(ctx context.Context, ex Executor, name, description, idGroup string) (*Agent, error) {
	it := item{"name": name, "description": description, "idGroup": nil}
	if idGroup != "" {
		it["idGroup"] = idGroup
	}
	items, err := commit(ctx, ex, "agent", []change{{Operation: "add", Index: 0, Item: it}})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("the service accepted the agent but returned no record, so there is no installation code to show")
	}
	return agentFromItem(items[0]), nil
}
