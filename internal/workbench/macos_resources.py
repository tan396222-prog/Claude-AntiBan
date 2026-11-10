"""Read macOS guest metrics through the public Mach and sysctl interfaces."""
import ctypes
import json
import os


lib = ctypes.CDLL("/usr/lib/libSystem.B.dylib", use_errno=True)
u32 = ctypes.c_uint32
u64 = ctypes.c_uint64


class VMStatistics(ctypes.Structure):
    # HOST_VM_INFO64_REV1_COUNT, available on every supported macOS guest.
    _fields_ = [
        ("free", u32), ("active", u32), ("inactive", u32), ("wired", u32),
        ("zero_fill", u64), ("reactivations", u64), ("pageins", u64),
        ("pageouts", u64), ("faults", u64), ("cow_faults", u64),
        ("lookups", u64), ("hits", u64), ("purges", u64),
        ("purgeable", u32), ("speculative", u32),
        ("decompressions", u64), ("compressions", u64),
        ("swapins", u64), ("swapouts", u64),
        ("compressor", u32), ("throttled", u32), ("external", u32),
        ("internal", u32), ("uncompressed", u64),
    ]


class BootTime(ctypes.Structure):
    _fields_ = [("seconds", ctypes.c_long), ("microseconds", ctypes.c_int32)]


class SwapUsage(ctypes.Structure):
    _fields_ = [("total", u64), ("available", u64), ("used", u64),
                ("page_size", u32), ("encrypted", ctypes.c_int32)]


lib.sysctlbyname.argtypes = [ctypes.c_char_p, ctypes.c_void_p,
                            ctypes.POINTER(ctypes.c_size_t), ctypes.c_void_p, ctypes.c_size_t]
lib.sysctlbyname.restype = ctypes.c_int
lib.mach_host_self.argtypes = []
lib.mach_host_self.restype = u32
lib.host_statistics.argtypes = [u32, ctypes.c_int, ctypes.c_void_p, ctypes.POINTER(u32)]
lib.host_statistics.restype = ctypes.c_int
lib.host_statistics64.argtypes = lib.host_statistics.argtypes
lib.host_statistics64.restype = ctypes.c_int


def sysctl(name, kind):
    value = kind()
    size = ctypes.c_size_t(ctypes.sizeof(value))
    if lib.sysctlbyname(name.encode(), ctypes.byref(value), ctypes.byref(size), None, 0) != 0:
        raise OSError(ctypes.get_errno(), "sysctl " + name)
    if size.value != ctypes.sizeof(value):
        raise ValueError("unexpected sysctl size for " + name)
    return value


host = lib.mach_host_self()
vm = VMStatistics()
count = u32(ctypes.sizeof(vm) // ctypes.sizeof(u32))
if lib.host_statistics64(host, 4, ctypes.byref(vm), ctypes.byref(count)) != 0 or count.value != 38:
    raise OSError("host_statistics64 did not return complete VM counters")
ticks = (u32 * 4)()
count = u32(4)
if lib.host_statistics(host, 3, ctypes.byref(ticks), ctypes.byref(count)) != 0 or count.value != 4:
    raise OSError("host_statistics did not return complete CPU counters")
boot = sysctl("kern.boottime", BootTime)
page_size = os.sysconf("SC_PAGE_SIZE")
# Mach free_count already includes speculative_count. Inactive pages form an
# availability estimate and are counted once, separately from compressed pages.
result = {
    "version": 1,
    "total_bytes": sysctl("hw.memsize", u64).value,
    "available_bytes": (vm.free + vm.inactive) * page_size,
    "free_bytes": vm.free * page_size,
    "inactive_bytes": vm.inactive * page_size,
    "wired_bytes": vm.wired * page_size,
    "compressed_bytes": vm.compressor * page_size,
    "cpus": sysctl("hw.logicalcpu", u32).value,
    "cpu_ticks": list(ticks),
    "boot_seconds": boot.seconds,
}
try:
    swap = sysctl("vm.swapusage", SwapUsage)
    result.update(swap_total_bytes=swap.total, swap_free_bytes=swap.available)
except OSError:
    pass
try:
    result["pressure_level"] = sysctl("kern.memorystatus_vm_pressure_level", u32).value
except OSError:
    pass
print(json.dumps(result, separators=(",", ":")))
