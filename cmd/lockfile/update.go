package main

func init() {
	// The launcher's: run refuses it in the image (hostModeRefusal). It is
	// here for the help only.
	commands["update"] = command{human: true, help: "[vX.Y.Z]: update the launcher, its seccomp profile and its image (on the host)", run: func(e env, args []string) int { return refuseHostMode(e, "update") }}
}
