Cage for Windows 10 and Windows 11 (x64)

Install Cage for the current Windows user, then open it from the Start menu.
No Go, Node.js, .NET SDK, QEMU, OpenSSH or Clash installation is required.
The installer includes the Microsoft WebView2 offline runtime installer.

Requirements: Windows 10 22H2 (build 19045) or Windows 11, Intel/AMD 64-bit
processor with hardware virtualization enabled, and Windows Hypervisor Platform.
Enable Windows Hypervisor Platform in "Turn Windows features on or off", then
restart Windows if Cage reports that the hypervisor is unavailable.
Windows on ARM and 32-bit Windows are not supported by this package.

In Cage, choose an environment data folder before starting the environment if
you want the Linux image, cache and virtual disks on another drive. Configure
your own proxy and start the environment to download the Linux image and tools.
Proxy service and Claude account are not included. Changes to shared projects
affect the original files.

Updates preserve the Cage configuration, virtual disk and project files.
Stop the environment and quit Cage before running an installer manually.
In-app updates stop the environment after your confirmation, verify the new
installer and publisher, and restart the application after installation.
Unsigned development builds cannot install automatic updates.

Uninstall using Windows Settings > Apps > Installed apps (Windows 11) or
Apps & features (Windows 10). Uninstall removes application files and shortcuts.
It preserves user data in %USERPROFILE%\.claude-cage and all shared projects.
Microsoft WebView2 is a shared runtime and is not removed by Cage uninstall.

Getting started: https://github.com/tan396222-prog/Claude-AntiBan/blob/main/docs/windows.md
Issues: https://github.com/tan396222-prog/Claude-AntiBan/issues
