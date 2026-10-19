// Package pluginspec is the lockfile plugin contract: the signed manifest a
// plugin ships with, and the rule that turns a plugin's stdout and exit
// status into an answer.
//
// A plugin answers exactly one yes/no question about one file. Its only
// output is the byte 1 or 0 on stdout (one trailing newline tolerated)
// followed by exit status 0. Anything else is a failure, and every
// plugin-caused failure reaches the agent as the single code
// AgentErrorCode; the precise cause is kept for the audit only.
package pluginspec
