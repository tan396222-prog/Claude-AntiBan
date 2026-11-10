//go:build darwin && cgo

package hostproxy

/*
#cgo LDFLAGS: -framework CoreFoundation -framework CFNetwork
#include <CFNetwork/CFNetwork.h>
#include <stdlib.h>
#include <string.h>

typedef struct { char *url; int failed; } CageProxy;
static char *cageString(CFStringRef value) {
    if (!value) return NULL;
    CFIndex size = CFStringGetMaximumSizeForEncoding(CFStringGetLength(value), kCFStringEncodingUTF8) + 1;
    char *text = calloc(size, 1);
    if (!CFStringGetCString(value, text, size, kCFStringEncodingUTF8)) { free(text); return NULL; }
    return text;
}
static void cagePACCallback(void *client, CFArrayRef proxies, CFErrorRef error) {
    CFTypeRef *result = client;
    *result = CFRetain(error ? (CFTypeRef)error : (CFTypeRef)proxies);
    CFRunLoopStop(CFRunLoopGetCurrent());
}
static CageProxy cageResolveArray(CFArrayRef proxies, CFURLRef target, int allowPAC) {
    CageProxy result = {NULL, 1};
    if (!proxies || !CFArrayGetCount(proxies)) return result;
    CFDictionaryRef proxy = CFArrayGetValueAtIndex(proxies, 0);
    CFStringRef type = CFDictionaryGetValue(proxy, kCFProxyTypeKey);
    if (!type) return result;
    if (CFEqual(type, kCFProxyTypeNone)) { result.failed = 0; return result; }
    if (allowPAC && CFEqual(type, kCFProxyTypeAutoConfigurationURL)) {
        CFURLRef pac = CFDictionaryGetValue(proxy, kCFProxyAutoConfigurationURLKey);
        if (!pac) return result;
        CFTypeRef answer = NULL;
        CFStreamClientContext context = {0, &answer, NULL, NULL, NULL};
        CFRunLoopSourceRef source = CFNetworkExecuteProxyAutoConfigurationURL(pac, target, cagePACCallback, &context);
        if (source) {
            CFStringRef mode = CFSTR("CageImageProxy");
            CFRunLoopAddSource(CFRunLoopGetCurrent(), source, mode);
            CFRunLoopRunInMode(mode, 15, false);
            CFRunLoopRemoveSource(CFRunLoopGetCurrent(), source, mode);
            CFRunLoopSourceInvalidate(source);
            CFRelease(source);
        }
        if (answer) {
            if (CFGetTypeID(answer) == CFArrayGetTypeID()) result = cageResolveArray((CFArrayRef)answer, target, 0);
            CFRelease(answer);
        }
        return result;
    }
    const char *scheme = NULL;
    if (CFEqual(type, kCFProxyTypeHTTP)) scheme = "http";
    // CFNetwork's HTTPS type means HTTP CONNECT, not TLS to the proxy.
    if (CFEqual(type, kCFProxyTypeHTTPS)) scheme = "http";
    if (CFEqual(type, kCFProxyTypeSOCKS)) scheme = "socks5";
    if (!scheme) return result;
    char *host = cageString(CFDictionaryGetValue(proxy, kCFProxyHostNameKey));
    int port = 0;
    CFNumberRef number = CFDictionaryGetValue(proxy, kCFProxyPortNumberKey);
    if (number) CFNumberGetValue(number, kCFNumberIntType, &port);
    if (host && port > 0 && port <= 65535) {
        CFStringRef url = CFStringCreateWithFormat(NULL, NULL, CFSTR("%s://%s%s%s:%d"), scheme,
            strchr(host, ':') ? "[" : "", host, strchr(host, ':') ? "]" : "", port);
        result.url = cageString(url);
        result.failed = result.url == NULL;
        CFRelease(url);
    }
    free(host);
    return result;
}
static CageProxy cageSystemProxy(char *raw) {
    CageProxy result = {NULL, 1};
    CFStringRef text = CFStringCreateWithCString(NULL, raw, kCFStringEncodingUTF8);
    CFURLRef url = text ? CFURLCreateWithString(NULL, text, NULL) : NULL;
    CFDictionaryRef settings = CFNetworkCopySystemProxySettings();
    if (url && settings) {
        CFArrayRef proxies = CFNetworkCopyProxiesForURL(url, settings);
        result = cageResolveArray(proxies, url, 1);
        if (proxies) CFRelease(proxies);
    }
    if (settings) CFRelease(settings);
    if (url) CFRelease(url);
    if (text) CFRelease(text);
    return result;
}
*/
import "C"

import (
	"errors"
	"net/http"
	"net/url"
	"runtime"
	"unsafe"
)

func systemProxy(req *http.Request) (*url.URL, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	// CFNetwork's PAC callback and run loop belong to the same OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	raw := C.CString(req.URL.String())
	defer C.free(unsafe.Pointer(raw))
	result := C.cageSystemProxy(raw)
	defer C.free(unsafe.Pointer(result.url))
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if result.failed != 0 {
		return nil, errors.New("无法解析主机系统代理或 PAC，请检查 Clash 等代理软件后重试；未切换直连")
	}
	if result.url == nil {
		return nil, nil
	}
	return proxyURL(C.GoString(result.url))
}
