using System.Runtime.InteropServices;
using Windows.Foundation;
using Windows.Graphics.Capture;
using Windows.Graphics.DirectX;
using Windows.Graphics.DirectX.Direct3D11;
using Windows.Graphics.Imaging;
using Windows.Storage.Streams;
using WinRT;

namespace Cage.GuestClipboard;

// Capture items are created exclusively for the caller's validated Claude HWND.
// No monitor item, desktop duplication or screen BitBlt is available here.
internal static class ClaudeGraphicsCapture
{
    private static readonly Guid ItemInterop = new("3628e81b-3cac-4c60-b7f4-23ce0e0c3356");
    private static readonly Guid ItemInterface = new("79c3f95b-31f7-4ec2-a464-632ef5d30760");
    private static readonly Guid DxgiDevice = new("54ec77fa-1377-44e6-8c32-88fd5f44c84c");
    private static readonly SemaphoreSlim CaptureGate = new(1, 1);
    private static readonly System.Threading.Timer Idle = new(state => { _ = Expire(); });
    private static CaptureSession? current;

    private static async Task Expire()
    {
        if (!await CaptureGate.WaitAsync(0)) return;
        try { current?.Dispose(); current = null; }
        finally { CaptureGate.Release(); }
    }

    internal static async Task<byte[]> Capture(IntPtr window, int pid, int width, int height, Action validate, bool allowSolid = false, bool preserveAlpha = false)
    {
        await CaptureGate.WaitAsync();
        Idle.Change(Timeout.Infinite, Timeout.Infinite);
        try
        {
            validate();
            try { if (!GraphicsCaptureSession.IsSupported()) throw new InvalidDataException("desktop_graphics_capture_unavailable"); }
            catch (COMException error) when ((uint)error.HResult == 0x80070005)
            { throw new InvalidDataException("desktop_capture_permission_required", error); }
            Exception? hardware = null;
            foreach (uint driver in new uint[] { 1, 5 }) // hardware, then WARP
            {
                try { return await CaptureDevice(window, pid, width, height, validate, driver, allowSolid, preserveAlpha); }
                catch (COMException error) when ((uint)error.HResult == 0x80070005)
                { throw new InvalidDataException("desktop_capture_permission_required", error); }
                catch (Exception error) when (error is COMException || error is TimeoutException || error is InvalidDataException { Message: "desktop_empty_frame" })
                {
                    current?.Dispose();
                    current = null;
                    hardware = error;
                }
            }
            throw new InvalidDataException(hardware is InvalidDataException ? "desktop_empty_frame" : "desktop_graphics_device_unavailable", hardware);
        }
        finally { Idle.Change(1500, Timeout.Infinite); CaptureGate.Release(); }
    }
    private static async Task<byte[]> CaptureDevice(IntPtr window, int pid, int width, int height, Action validate, uint driver, bool allowSolid, bool preserveAlpha)
    {
        if (current is null || !current.Matches(window, pid, width, height, allowSolid, preserveAlpha))
        {
            current?.Dispose(); current = null;
            current = new CaptureSession(window, pid, width, height, driver, allowSolid, preserveAlpha);
        }
        return await current.Read(validate);
    }

    private sealed class CaptureSession : IDisposable
    {
        private readonly IntPtr window;
        private readonly int pid, width, height;
        private readonly bool allowSolid, preserveAlpha;
        private readonly GraphicsCaptureItem item;
        private readonly IDirect3DDevice device;
        private Direct3D11CaptureFramePool? pool;
        private GraphicsCaptureSession? capture;
        private readonly object frames = new();
        private TaskCompletionSource<bool> arrived = new(TaskCreationOptions.RunContinuationsAsynchronously);
        private Direct3D11CaptureFrame? latest;
        private byte[]? encoded;
        private Windows.Storage.Streams.Buffer? pixelBuffer;
        private byte[] pixelsRead = [];
        private byte[]? previousPixels;
        private TimeSpan? timestamp;
        private bool closed;

        internal CaptureSession(IntPtr window, int pid, int width, int height, uint driver, bool allowSolid, bool preserveAlpha)
        {
            this.window = window; this.pid = pid; this.width = width; this.height = height; this.allowSolid = allowSolid; this.preserveAlpha = preserveAlpha;
            item = Item(window);
            if (item.Size.Width < width || item.Size.Height < height || (long)item.Size.Width * item.Size.Height > 40_000_000)
                throw new InvalidDataException("desktop_target_changed");
            device = Device(driver);
            try
            {
                pool = Direct3D11CaptureFramePool.CreateFreeThreaded(device, DirectXPixelFormat.B8G8R8A8UIntNormalized, 2, item.Size);
                capture = pool.CreateCaptureSession(item);
                capture.IsCursorCaptureEnabled = false;
                pool.FrameArrived += OnFrame;
                item.Closed += OnClosed;
                capture.StartCapture();
            }
            catch { Dispose(); throw; }
        }

        internal bool Matches(IntPtr handle, int process, int w, int h, bool solid, bool alpha)
        {
            lock (frames) return !closed && window == handle && pid == process && width == w && height == h && allowSolid == solid && preserveAlpha == alpha;
        }

        private void OnFrame(Direct3D11CaptureFramePool sender, object args)
        {
            try
            {
                var frame = sender.TryGetNextFrame();
                if (frame is null) return;
                lock (frames)
                {
                    if (closed) { frame.Dispose(); return; }
                    latest?.Dispose();
                    latest = frame;
                    arrived.TrySetResult(true);
                }
            }
            catch (Exception error)
            {
                lock (frames)
                {
                    closed = true;
                    latest?.Dispose(); latest = null;
                    arrived.TrySetException(error);
                }
            }
        }

        private void OnClosed(GraphicsCaptureItem sender, object args)
        {
            lock (frames) { closed = true; arrived.TrySetException(new InvalidDataException("desktop_target_changed")); }
        }

        internal async Task<byte[]> Read(Action validate)
        {
            validate();
            var geometry = Geometry(window, pid, width, height);
            Task ready;
            lock (frames)
            {
                if (closed) throw new InvalidDataException("desktop_target_changed");
                if (latest is null && encoded is not null) return encoded;
                ready = arrived.Task;
            }
            await ready.WaitAsync(TimeSpan.FromSeconds(3));
            Direct3D11CaptureFrame frame;
            lock (frames)
            {
                if (closed || latest is null) throw new InvalidDataException("desktop_target_changed");
                frame = latest; latest = null;
                arrived = new(TaskCreationOptions.RunContinuationsAsynchronously);
            }
            using (frame)
            {
                if (encoded is not null && timestamp == frame.SystemRelativeTime) return encoded;
                using var bitmap = await SoftwareBitmap.CreateCopyFromSurfaceAsync(frame.Surface, preserveAlpha ? BitmapAlphaMode.Premultiplied : BitmapAlphaMode.Ignore);
                validate();
                var after = Geometry(window, pid, width, height);
                if (after != geometry) throw new InvalidDataException("desktop_target_changed");
                var source = geometry.Source(frame.ContentSize.Width, frame.ContentSize.Height);
                using var pixels = SoftwareBitmap.Convert(bitmap, BitmapPixelFormat.Bgra8, preserveAlpha ? BitmapAlphaMode.Straight : BitmapAlphaMode.Ignore);
                var byteCount = checked(pixels.PixelWidth * pixels.PixelHeight * 4);
                if (pixelsRead.Length != byteCount)
                {
                    pixelsRead = new byte[byteCount];
                    pixelBuffer = new Windows.Storage.Streams.Buffer((uint)byteCount);
                }
                pixels.CopyToBuffer(pixelBuffer!);
                using (var reader = DataReader.FromBuffer(pixelBuffer!)) reader.ReadBytes(pixelsRead);
                var rowBytes = checked(width * 4);
                var identical = previousPixels is not null;
                for (var row = 0; identical && row < height; row++)
                {
                    var offset = ((source.Y + row) * pixels.PixelWidth + source.X) * 4;
                    identical = pixelsRead.AsSpan(offset, rowBytes).SequenceEqual(previousPixels!.AsSpan(row * rowBytes, rowBytes));
                }
                if (identical)
                {
                    validate();
                    timestamp = frame.SystemRelativeTime;
                    return encoded!;
                }
                var png = await EncodeClient(pixels, source.X, source.Y, width, height);
                validate();
                using var input = new MemoryStream(png, false);
                using var client = new Bitmap(input);
                if (client.Width != width || client.Height != height || (!allowSolid && !HasContent(client))) throw new InvalidDataException("desktop_empty_frame");
                // Publish the comparison pixels only after encoding and target
                // validation succeed, so a failed frame cannot reuse old PNGs.
                previousPixels ??= new byte[checked(rowBytes * height)];
                for (var row = 0; row < height; row++)
                {
                    var offset = ((source.Y + row) * pixels.PixelWidth + source.X) * 4;
                    pixelsRead.AsSpan(offset, rowBytes).CopyTo(previousPixels.AsSpan(row * rowBytes, rowBytes));
                }
                timestamp = frame.SystemRelativeTime;
                encoded = png;
                return png;
            }
        }

        public void Dispose()
        {
            lock (frames)
            {
                closed = true;
                latest?.Dispose(); latest = null;
                arrived.TrySetCanceled();
            }
            if (pool is not null) pool.FrameArrived -= OnFrame;
            item.Closed -= OnClosed;
            capture?.Dispose(); pool?.Dispose(); device.Dispose();
        }
    }
    internal static bool HasContent(Bitmap bitmap)
    {
        // A white/black client plus custom title buttons is still an empty frame.
        int left = bitmap.Width / 12, right = bitmap.Width - left;
        int top = bitmap.Height * 15 / 100, bottom = bitmap.Height * 95 / 100;
        var first = bitmap.GetPixel(left, top); int varied = 0;
        for (int y = top; y < bottom; y += Math.Max(1, (bottom-top)/64))
            for (int x = left; x < right; x += Math.Max(1, (right-left)/64))
            {
                var pixel = bitmap.GetPixel(x, y);
                if (Math.Abs(pixel.R-first.R)>8 || Math.Abs(pixel.G-first.G)>8 || Math.Abs(pixel.B-first.B)>8) varied++;
            }
        return varied >= 8;
    }
    private static async Task<byte[]> EncodeClient(SoftwareBitmap source, int x, int y, int width, int height)
    {
        if (x < 0 || y < 0 || x + width > source.PixelWidth || y + height > source.PixelHeight)
            throw new InvalidDataException("desktop_target_changed");
        using var output = new InMemoryRandomAccessStream();
        var encoder = await BitmapEncoder.CreateAsync(BitmapEncoder.PngEncoderId, output);
        encoder.SetSoftwareBitmap(source);
        encoder.BitmapTransform.Bounds = new BitmapBounds { X = (uint)x, Y = (uint)y, Width = (uint)width, Height = (uint)height };
        await encoder.FlushAsync();
        if (output.Size is 0 or > Protocol.PngLimit) throw new InvalidDataException("desktop_frame_limit");
        using var input = output.GetInputStreamAt(0);
        using var reader = new DataReader(input);
        var length = checked((uint)output.Size);
        if (await reader.LoadAsync(length) != length) throw new InvalidDataException("desktop_capture_buffer_invalid");
        var bytes = new byte[length];
        reader.ReadBytes(bytes);
        return bytes;
    }

    private static GraphicsCaptureItem Item(IntPtr window)
    {
        using var factory = ActivationFactory.Get("Windows.Graphics.Capture.GraphicsCaptureItem", ItemInterop);
        var function = Marshal.ReadIntPtr(Marshal.ReadIntPtr(factory.ThisPtr),3*IntPtr.Size);
        var create = Marshal.GetDelegateForFunctionPointer<CreateWindow>(function);
        var iid = ItemInterface;
        Marshal.ThrowExceptionForHR(create(factory.ThisPtr,window,ref iid,out var value));
        try { return MarshalInterface<GraphicsCaptureItem>.FromAbi(value); } finally { Marshal.Release(value); }
    }
    private static IDirect3DDevice Device(uint driver)
    {
        IntPtr native=IntPtr.Zero, context=IntPtr.Zero, dxgi=IntPtr.Zero, wrapped=IntPtr.Zero;
        try
        {
            Marshal.ThrowExceptionForHR(D3D11CreateDevice(IntPtr.Zero,driver,IntPtr.Zero,0x20,IntPtr.Zero,0,7,out native,out _,out context));
            var iid=DxgiDevice;Marshal.ThrowExceptionForHR(Marshal.QueryInterface(native,in iid,out dxgi));
            Marshal.ThrowExceptionForHR(CreateDirect3D11DeviceFromDXGIDevice(dxgi,out wrapped));
            return MarshalInterface<IDirect3DDevice>.FromAbi(wrapped);
        }
        finally
        {
            if(wrapped!=IntPtr.Zero)Marshal.Release(wrapped);if(dxgi!=IntPtr.Zero)Marshal.Release(dxgi);
            if(context!=IntPtr.Zero)Marshal.Release(context);if(native!=IntPtr.Zero)Marshal.Release(native);
        }
    }
    private readonly record struct Snapshot(Rect Window,Rect Visible,int ClientX,int ClientY,int Width,int Height)
    {
        internal Point Source(int width,int height)
        {
            Rect frame;
            if(Visible.Right-Visible.Left==width && Visible.Bottom-Visible.Top==height)frame=Visible;
            else if(Window.Right-Window.Left==width && Window.Bottom-Window.Top==height)frame=Window;
            else throw new InvalidDataException("desktop_target_changed");
            return new(ClientX-frame.Left,ClientY-frame.Top);
        }
    }
    private static Snapshot Geometry(IntPtr window,int pid,int width,int height)
    {
        var prior=SetThreadDpiAwarenessContext(new IntPtr(-4));
        try
        {
            if(!SessionGuard.Available() || GetWindowThreadProcessId(window,out var actual)==0 || actual!=pid ||
                !GetWindowRect(window,out var rectangle) || !GetClientRect(window,out var client) || client.Right!=width || client.Bottom!=height)
                throw new InvalidDataException("desktop_target_changed");
            var visible=rectangle;if(DwmGetWindowAttribute(window,9,out var drawn,Marshal.SizeOf<Rect>())==0)visible=drawn;
            var origin=new Point();if(!ClientToScreen(window,ref origin))throw new InvalidDataException("desktop_target_changed");
            return new(rectangle,visible,origin.X,origin.Y,width,height);
        }
        finally {if(prior!=IntPtr.Zero)SetThreadDpiAwarenessContext(prior);}
    }
    [StructLayout(LayoutKind.Sequential)] private readonly record struct Rect(int Left,int Top,int Right,int Bottom);
    [StructLayout(LayoutKind.Sequential)] private struct Point {internal int X,Y;internal Point(int x,int y){X=x;Y=y;}}
    [UnmanagedFunctionPointer(CallingConvention.StdCall)] private delegate int CreateWindow(IntPtr factory,IntPtr window,ref Guid iid,out IntPtr item);
    [DefaultDllImportSearchPaths(DllImportSearchPath.System32)]
    [DllImport("d3d11.dll")] private static extern int D3D11CreateDevice(IntPtr adapter,uint driver,IntPtr software,uint flags,IntPtr features,uint featureCount,uint sdk,out IntPtr device,out uint level,out IntPtr context);
    [DefaultDllImportSearchPaths(DllImportSearchPath.System32)]
    [DllImport("d3d11.dll")] private static extern int CreateDirect3D11DeviceFromDXGIDevice(IntPtr dxgi,out IntPtr device);
    [DefaultDllImportSearchPaths(DllImportSearchPath.System32)]
    [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr window,out uint pid);
    [DefaultDllImportSearchPaths(DllImportSearchPath.System32)]
    [DllImport("user32.dll")] private static extern bool GetClientRect(IntPtr window,out Rect rect);
    [DefaultDllImportSearchPaths(DllImportSearchPath.System32)]
    [DllImport("user32.dll")] private static extern bool GetWindowRect(IntPtr window,out Rect rect);
    [DefaultDllImportSearchPaths(DllImportSearchPath.System32)]
    [DllImport("user32.dll")] private static extern bool ClientToScreen(IntPtr window,ref Point point);
    [DefaultDllImportSearchPaths(DllImportSearchPath.System32)]
    [DllImport("user32.dll")] private static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
    [DefaultDllImportSearchPaths(DllImportSearchPath.System32)]
    [DllImport("dwmapi.dll")] private static extern int DwmGetWindowAttribute(IntPtr window,uint attribute,out Rect rect,int size);
}
