# lockfile

lockfile lets an AI agent ask yes/no questions about a file it must never
read. A separate OS account fetches the file from S3, SFTP or a local
directory, runs signed, sandboxed plugins on it, and returns only each
plugin's answer: `1` or `0`. No byte of the file, its name, size, type or
path ever reaches the agent. Plugins come from a signed marketplace
([lockfile-marketplace](https://github.com/DavidGodefroid/lockfile-marketplace)).

Status: early development; the console, CLI and MCP server are not there yet.
