package manage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Group is an agent group on the account.
type Group struct {
	ID   string
	Name string
}

// ListGroups reads the account's groups, which use the same datasource shape as
// agents on their own module.
func ListGroups(ctx context.Context, ex Executor) ([]Group, error) {
	raw, err := ex.Execute(ctx, "group", "datasource", map[string]string{"operation": "load"})
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	var res struct {
		Items []struct {
			ID   string `json:"_id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("parse groups: %w", err)
	}
	out := make([]Group, 0, len(res.Items))
	for _, it := range res.Items {
		out = append(out, Group{ID: it.ID, Name: it.Name})
	}
	return out, nil
}

// ResolveGroup matches a group by exact name. Creating groups is out of scope,
// so an unknown name is an error that shows what does exist: a typo should not
// quietly become a new group on the account.
func ResolveGroup(groups []Group, name string) (*Group, error) {
	for i := range groups {
		if groups[i].Name == name {
			return &groups[i], nil
		}
	}
	names := make([]string, 0, len(groups))
	for _, g := range groups {
		names = append(names, g.Name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("no group named %q; this account has no groups", name)
	}
	return nil, fmt.Errorf("no group named %q; existing groups: %s", name, strings.Join(names, ", "))
}
