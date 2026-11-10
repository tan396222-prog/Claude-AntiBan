Cage for Windows 10 and Windows 11 (x64)

Install Cage for the current Windows user. On the Shortcuts page, choose a
desktop shortcut and Start menu entries. Both are selected on a fresh install.
Upgrades remember the selection. Open Cage from either selected location.
For a first installation, the default is D:\Cage when D: is a local fixed
drive, or %LOCALAPPDATA%\Programs\Cage otherwise. Upgrades keep the existing
installation folder. You can choose another location in the installer.
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
Projects and links inside the installation folder are retained. Modified files
that conflict with the new program, and unrecognized libraries in program load
directories, are kept in Cage-preserved-files inside that folder. Changes made
during installation can also be retained in its late-* subdirectories.
If recovery is interrupted, a .cage-installation.json record beside the
application folder identifies the retained installation, staged and backup
folders. Keep the record and those folders until recovery is complete.
Automatic updates stop application launch while recovery remains incomplete.
Stop the environment and quit Cage before running an installer manually.
In-app updates stop the environment after your confirmation, verify the new
installer and publisher, and restart the application after installation.
Unsigned development builds cannot install automatic updates.

Uninstall using Windows Settings > Apps > Installed apps (Windows 11) or
Apps & features (Windows 10). Uninstall removes application files and shortcuts.
It preserves user data in %USERPROFILE%\.claude-cage and all shared projects.
If personal files remain inside the installation folder, the folder remains
available for a later installation.
Microsoft WebView2 is a shared runtime and is not removed by Cage uninstall.

Getting started: https://github.com/tan396222-prog/Claude-AntiBan/blob/main/docs/windows.md
Issues: https://github.com/tan396222-prog/Claude-AntiBan/issues
