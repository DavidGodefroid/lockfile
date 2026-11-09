package clientcmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/DavidGodefroid/lockkit/client"
	"github.com/DavidGodefroid/lockkit/sanitize"

	"github.com/DavidGodefroid/lockfile/internal/product"
)

// instance is the lock instance every call reaches (SetInstance): ""
// is the socket directory itself, as before instances.
var instance string

// instances are the locks lea mounts when it mounts several and none is
// chosen (SetInstances): each call reaches the one that serves its
// profile. Empty, every call reaches instance.
var instances []string

// instanceErr is why the lock instance could not be resolved
// (SetInstanceError): every call fails with it, so that the MCP server
// starts and its tools, status first, say what is wrong.
var instanceErr *Error

// SetInstance sets the lock instance every call reaches, the one
// ResolveInstances resolved once per process (the CLI after its flags,
// mcp before it serves).
func SetInstance(name string) { instance, instances, instanceErr = name, nil, nil }

// SetInstances makes every call reach, among names, the lock that serves
// its profile: lea mounts several locks of this type and none is chosen.
func SetInstances(names []string) {
	instance, instances, instanceErr = "", append([]string(nil), names...), nil
}

// SetInstanceError makes every call fail with err, the error of
// ResolveInstances: its text, which never quotes a name that is not a
// lock instance name, is the message.
func SetInstanceError(err error) {
	instance, instances = "", nil
	instanceErr = &Error{Kind: KindOther, Msg: "lockfile: " + sanitize.Terminal(err.Error(), false)}
}

// Instance env variables: the one that picks the lock, and the list lea
// sets when it mounts several (lockkit's client.ResolveInstance).
var (
	instanceEnv  = product.P.Env("INSTANCE")
	instancesEnv = "LEA_" + product.P.Env("INSTANCES")
)

// ResolveInstances is the lock instance the client reaches, as lockkit's
// client.ResolveInstance resolves it (flag, else $LOCKFILE_INSTANCE, else
// the one lock lea lists), or, when lea lists several and neither the flag
// nor the variable chooses one, all of them: each call then reaches the
// lock that serves its profile (profile names are unique in a workspace).
func ResolveInstances(flag string) (string, []string, error) {
	inst, err := client.ResolveInstance(product.P, flag)
	if err == nil {
		return inst, nil, nil
	}
	list := strings.Fields(os.Getenv(instancesEnv))
	if errors.Is(err, client.ErrBadInstance) || flag != "" || os.Getenv(instanceEnv) != "" || len(list) < 2 {
		return "", nil, err
	}
	return "", list, nil
}

// ApplyInstances resolves the lock instances (ResolveInstances) and sets
// them for every call; it returns the resolution's error, also set.
func ApplyInstances(flag string) error {
	inst, list, err := ResolveInstances(flag)
	switch {
	case err != nil:
		SetInstanceError(err)
	case list != nil:
		SetInstances(list)
	default:
		SetInstance(inst)
	}
	return err
}

// instanceOf is the lock instance that serves profile: instance, or with
// several locks the first whose socket for profile is there. None is
// client.ErrNoConsole, which closed turns into unknown_profile.
func instanceOf(profile string) (string, error) {
	if len(instances) == 0 {
		return instance, nil
	}
	for _, inst := range instances {
		if socketExistsIn(inst, profile) {
			return inst, nil
		}
	}
	return "", fmt.Errorf("profile on no listed lock: %w", client.ErrNoConsole)
}

// reached is every lock instance a listing covers: instances, or instance.
func reached() []string {
	if len(instances) > 0 {
		return instances
	}
	return []string{instance}
}
