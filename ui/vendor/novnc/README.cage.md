# noVNC source used by the trusted workbench

Upstream: https://github.com/novnc/noVNC/tree/v1.6.0
Release archive SHA-256: 5066103959ef4e9b10f37e5a148627360dd8414e4cf8a7db92bdbd022e728aaa

Only core, vendor and license files are retained. This source is obtained from the official release, never copied from a mutable guest. The build copies it into `.generated/novnc` and applies `internal/cage/novnc-patch.py`, the same patch used by the legacy VM viewer. The patch serializes clipboard transfers and lets the workbench attach an editable keyboard target for host IME composition. Legacy viewers retain their canvas keyboard target. Modified RFB code retains its MPL-2.0 header; the complete modification source is shipped in this repository.
