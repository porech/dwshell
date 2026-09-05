package manage

import (
	"context"
	"encoding/json"
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

// DeleteAgent removes an agent from the account. The service wants the record's
// id under both keys it uses internally.
func DeleteAgent(ctx context.Context, ex Executor, id string) error {
	_, err := commit(ctx, ex, "agent", []change{{
		Operation: "delete", Index: 0, Item: item{"_id": id, "id": id},
	}})
	return err
}

// ReinstallAgent puts an installed agent back into "pending installation" with
// a fresh code, invalidating the previous one. The command answers with a bare
// acknowledgement, so read the new code from the listing afterwards.
func ReinstallAgent(ctx context.Context, ex Executor, id string) error {
	_, err := ex.Execute(ctx, "agent", "reinstall", map[string]string{"id": id})
	return err
}

// loadAgentItem reads one agent's full record, with its position in the
// listing. Both are needed to update it: the service rejects a change carrying
// only the altered fields, answering java.lang.NullPointerException, so the
// whole record has to travel — which is what the browser client sends, since it
// merges the edit into the item it loaded.
func loadAgentItem(ctx context.Context, ex Executor, agentID string) (item, int, error) {
	raw, err := ex.Execute(ctx, "agent", "datasource", map[string]string{"operation": "load"})
	if err != nil {
		return nil, 0, fmt.Errorf("load agents: %w", err)
	}
	var res struct {
		Items []item `json:"items"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, 0, fmt.Errorf("parse agents: %w", err)
	}
	for i, it := range res.Items {
		if it["id"] == agentID || it["_id"] == agentID {
			return it, i, nil
		}
	}
	return nil, 0, fmt.Errorf("agent %s is not in the account listing", agentID)
}

// SetAgentGroup moves an agent into a group, or out of every group when idGroup
// is empty. The record is read back first because the service wants the whole
// item, not just the field being changed.
func SetAgentGroup(ctx context.Context, ex Executor, agentID, idGroup string) error {
	it, idx, err := loadAgentItem(ctx, ex, agentID)
	if err != nil {
		return err
	}
	it["idGroup"] = nil
	if idGroup != "" {
		it["idGroup"] = idGroup
	}
	_, err = commit(ctx, ex, "agent", []change{{Operation: "update", Index: idx, Item: it}})
	return err
}
