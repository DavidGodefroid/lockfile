package backend

import (
	"fmt"

	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/managed"

	"github.com/DavidGodefroid/lockfile/internal/profile"
)

var _ console.ManagedBackend = (*Backend)(nil)

// ManagedBounds implements console.ManagedBackend: the console checks the
// policy against [lockfile.bounds] at start and at every poll
// (profile.ManagedBounds).
func (b *Backend) ManagedBounds(p console.Policy, m *managed.File) (managed.Bounds, error) {
	pol, ok := p.(*profile.Policy)
	if !ok {
		return managed.Bounds{}, fmt.Errorf("not a lockfile policy: %T", p)
	}
	return profile.ManagedBounds(pol, m)
}
