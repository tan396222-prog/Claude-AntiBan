package guestos

import "context"

func Inspect(ctx context.Context) Inspection {
	ok, reason := probeWindowsHost(ctx)
	return Inspection{Guest: Windows11, HostCheckPassed: ok, WorkbenchSupport: nativeWorkbenchSupport(Windows11, ok), Detail: reason}
}
