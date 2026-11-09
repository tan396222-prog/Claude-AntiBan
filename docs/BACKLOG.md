# Upstream agent-vm backlog

This file retains upstream ideas and their original status. It is historical reference, not the Cage roadmap or a statement of current platform support. Cage desktop targets Apple Silicon macOS; current usage and development entry points are in the [Cage documentation index](README.md).

Known upstream gaps and ideas follow.

## Windows, natively

Status: built, not yet tried.

agent-vm runs on Windows without Git Bash now, its paths handled as `C:/...`
and seen by the VM as `/c/...`, as in 0.2, and its tests run on any OS. Nothing
of it has run on a Windows machine yet: setup, the shares, the network
isolation, the port check.

## Review the agent's writes

Status: idea.

Every write of the VM goes through agent-vm's SFTP server. It could keep them
aside and let you review the diff, and apply all or part of it to the real
folder. A first step: the list of every path the VM wrote during a session.

## Run a VM inside the VM

Status: needs an M3.

A `--allow-nested-vm` flag, for testing VM tooling from in there. Lima supports
nested virtualization, but it is off by default and cannot be turned on
unconditionally: below an Apple M3, Lima fails to start the VM instead of
degrading, so every VM would stop starting. The flag would have to detect host
support first. Nothing here has an M3 to test it on. It would also hand the
agent a hypervisor interface.

## Run the VMs on another machine

Status: designed, not built.

One install on your laptop driving VMs on a box you rent. Close the laptop, the
agent keeps working. Open questions: addressing a VM by name rather than by
working directory, and who owns deleting a workspace.
