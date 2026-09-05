// Package manage changes what an account owns: creating agents, deleting them,
// regenerating their installation code, and moving them between groups.
//
// Reading and connecting stay in internal/remote, which every command depends
// on: the path used to resolve a name should not also carry the code that
// deletes machines.
//
// DWService exposes these as "datasources" — one per module, edited by
// submitting a batch of pending changes (PROTOCOL.md §9).
package manage

import (
	"context"
	"encoding/json"
	"fmt"
)

// Executor is the slice of a session this package needs, satisfied by
// *session.Session; a fake stands in for it in tests.
type Executor interface {
	Execute(ctx context.Context, module, command string, params map[string]string) ([]byte, error)
}

// item is one datasource record. It stays untyped because a change carries only
// the fields it touches, while the service echoes back the whole record.
type item map[string]any

// change is one pending edit within a commit.
type change struct {
	Operation string `json:"operation"` // add | update | delete
	Index     int    `json:"index"`
	Item      item   `json:"item"`
}

// commitResponse is the reply to operation=commit. A rejection arrives here,
// as a status other than "ok" with a message — not as a transport error.
type commitResponse struct {
	Status       string `json:"status"`
	Message      string `json:"message"`
	ItemsChanged []struct {
		Index int  `json:"index"`
		Item  item `json:"item"`
	} `json:"itemsChanged"`
}

// commit applies changes to a datasource module ("agent" or "group") and returns
// the records the service echoed back. For an add that is the created record,
// which is where a new agent's installation code arrives — so creating an agent
// and learning its code is a single round trip.
func commit(ctx context.Context, ex Executor, module string, changes []change) ([]item, error) {
	body, err := json.Marshal(changes)
	if err != nil {
		return nil, err
	}
	raw, err := ex.Execute(ctx, module, "datasource", map[string]string{
		"operation": "commit",
		"changes":   string(body),
	})
	if err != nil {
		return nil, err
	}
	var res commitResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("parse %s commit response: %w", module, err)
	}
	if res.Status != "ok" {
		if res.Message != "" {
			return nil, fmt.Errorf("%s", res.Message)
		}
		return nil, fmt.Errorf("the service rejected the %s change", module)
	}
	out := make([]item, 0, len(res.ItemsChanged))
	for _, c := range res.ItemsChanged {
		out = append(out, c.Item)
	}
	return out, nil
}
