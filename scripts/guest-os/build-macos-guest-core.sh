#!/bin/bash
# Add the small raw-object accessor needed by the hostagent's AppKit view to a
# private copy of the pinned VZ binding. The Go module cache stays read-only.
set -euo pipefail
cd "$(dirname "$0")/../.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mod=github.com/Code-Hex/vz/v3
version=$(go list -m -f '{{.Version}}' "$mod")
[[ $version == v3.7.1 ]] || { echo "Review the login view against VZ binding $version" >&2; exit 1; }
go mod download "$mod"
dir=$(go list -m -f '{{.Dir}}' "$mod")
[[ -n "$dir" && -d "$dir" ]] || { echo 'VZ binding source is unavailable after download.' >&2; exit 1; }
cp -R "$dir" "$work/vz"
chmod -R u+w "$work/vz"
cat > "$work/vz/cage_view_object_darwin.go" <<'GO'
//go:build darwin && cgo

package vz

import (
    "unsafe"
    "github.com/Code-Hex/vz/v3/internal/objc"
)

// CageViewObject borrows the VZVirtualMachine for the duration of RunGUI.
func (v *VirtualMachine) CageViewObject() unsafe.Pointer { return objc.Ptr(v) }
GO
cp go.mod "$work/trial.mod"
cp go.sum "$work/trial.sum"
go mod edit -modfile "$work/trial.mod" -replace "$mod=$work/vz"
GOFLAGS="${GOFLAGS:-} -modfile=$work/trial.mod" make cage VERSION="$1"
