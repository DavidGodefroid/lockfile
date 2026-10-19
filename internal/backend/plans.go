package backend

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/DavidGodefroid/lockfile/internal/profile"
	"github.com/DavidGodefroid/lockfile/internal/workspace"
)

// planTTL is how long an approved plan may wait for its run.
const planTTL = 10 * time.Minute

// plan is an approved plan, run at most once, by the client that asked.
type plan struct {
	id       string
	peerUID  int
	key      string
	handle   workspace.Handle
	plugins  []string
	pins     []profile.PluginPin
	policyFP string // fingerprint of the approved policy at approval
	expires  time.Time
}

type planTable struct {
	now func() time.Time
	mu  sync.Mutex
	m   map[string]*plan
}

func newPlanTable(now func() time.Time) *planTable {
	return &planTable{now: now, m: map[string]*plan{}}
}

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// newPlanID is "pl_" and 10 random base62 characters.
func newPlanID() (string, error) {
	var b [10]byte
	out := []byte("pl_")
	for len(out) < 13 {
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		for _, c := range b {
			// Rejection sampling keeps the alphabet uniform.
			if c < 248 && len(out) < 13 {
				out = append(out, base62[c%62])
			}
		}
	}
	return string(out), nil
}

func (t *planTable) add(p *plan) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for id, x := range t.m {
		if !now.Before(x.expires) {
			delete(t.m, id)
		}
	}
	p.expires = now.Add(planTTL)
	t.m[p.id] = p
}

// take removes and returns plan id when it is live and was approved for
// peerUID: a plan runs once. Another client's take leaves it in place.
func (t *planTable) take(id string, peerUID int) (*plan, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.m[id]
	if !ok {
		return nil, false
	}
	if p.peerUID != peerUID {
		// Another client: refused, and the plan stays for its owner.
		return nil, false
	}
	delete(t.m, id)
	if !t.now().Before(p.expires) {
		return nil, false
	}
	return p, true
}
