package remarkable

import (
	"context"
	"slices"
)

// Node is an entry with the names of its ancestor folders, root first.
type Node struct {
	Entry
	Path []string
}

// Walk lists the whole tree, depth first, folders before their contents.
// Any listing error aborts the walk, so a partial tree is never returned.
func (c *Client) Walk(ctx context.Context) ([]Node, error) {
	var nodes []Node
	seen := map[string]bool{}
	var walk func(folderID string, path []string) error
	walk = func(folderID string, path []string) error {
		entries, err := c.List(ctx, folderID)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if seen[e.ID] {
				continue // defensive: never loop on a malformed tree
			}
			seen[e.ID] = true
			nodes = append(nodes, Node{Entry: e, Path: path})
			if e.IsFolder() {
				if err := walk(e.ID, append(slices.Clip(path), e.VisibleName)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk("", nil); err != nil {
		return nil, err
	}
	return nodes, nil
}
