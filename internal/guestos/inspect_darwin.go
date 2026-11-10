package guestos

import "context"

func Inspect(ctx context.Context) Inspection {
	r := Inspection{Guest: MacOS, WorkbenchSupport: nativeWorkbenchSupport(MacOS, false)}
	image, err := probeMacHost(ctx)
	if err != nil {
		r.Detail = "无法查询本机兼容的 Apple 恢复镜像：" + err.Error()
		return r
	}
	r.HostCheckPassed = true
	r.WorkbenchSupport = nativeWorkbenchSupport(MacOS, true)
	r.Restore = &image
	r.Detail = "Apple 提供与此主机兼容的 macOS 恢复镜像。"
	return r
}
