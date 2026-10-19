package sandbox

import (
	seccomp "github.com/elastic/go-seccomp-bpf"
	llsys "github.com/landlock-lsm/go-landlock/landlock/syscall"
)

// Probe reports the Landlock ABI and seccomp support of this kernel, for
// doctor. err is ErrUnsupported when plugins cannot run here.
func Probe() (landlockABI int, seccompOK bool, err error) {
	abi, lerr := llsys.LandlockGetABIVersion()
	if lerr != nil {
		abi = 0
	}
	ok := seccomp.Supported()
	if abi < 1 || !ok {
		return abi, ok, ErrUnsupported
	}
	return abi, ok, nil
}
