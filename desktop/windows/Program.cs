using System.IO.Pipes;
using System.Security.Cryptography;
using System.Security.Principal;
using System.Text;
using System.Runtime.InteropServices;

namespace Cage.Windows;

internal static class Program
{
    public static string? SmokeOutput { get; private set; }
    [STAThread]
    private static int Main(string[] args)
    {
        if (args is ["--install-update"]) return UpdateInstaller.Run().GetAwaiter().GetResult();
        if (args is ["--smoke-test", var output] && Path.IsPathFullyQualified(output))
        {
            SmokeOutput = output;
            if (File.Exists(output)) File.Delete(output);
        }
        else if (args.Length != 0) return 2;
        AppDomain.CurrentDomain.UnhandledException += (_, e) => Backend.Report(e.ExceptionObject is Exception error ? error.ToString() : "Unhandled native exception").GetAwaiter().GetResult();
        Application.ThreadException += (_, e) => {
            Backend.Report(e.Exception.ToString()).GetAwaiter().GetResult();
            using var dialog = new ThreadExceptionDialog(e.Exception);
            if (dialog.ShowDialog() == DialogResult.Abort) Application.Exit();
        };
        TaskScheduler.UnobservedTaskException += (_, e) => _ = Backend.Report(e.Exception.ToString());
        ApplicationConfiguration.Initialize();
        if (RuntimeInformation.OSArchitecture != Architecture.X64 || Environment.OSVersion.Version.Build < 19045)
        {
            MessageBox.Show("Cage 需要 Windows 10 22H2（19045）或 Windows 11，x64（Intel/AMD）系统。", "Cage");
            return 1;
        }
        var sid = WindowsIdentity.GetCurrent().User?.Value ?? Environment.UserName;
        var key = Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(sid + "\0" + Path.GetFullPath(Backend.Root).TrimEnd(Path.DirectorySeparatorChar).ToUpperInvariant())))[..24];
        using var mutex = new Mutex(true, @"Local\Cage-" + key, out var owner);
        if (!owner)
        {
            if (SmokeOutput != null) return 3;
            try
            {
                using var client = new NamedPipeClientStream(".", "Cage-" + key, PipeDirection.Out, PipeOptions.CurrentUserOnly);
                client.Connect(3000); client.WriteByte(1);
            }
            catch (IOException) { MessageBox.Show("Cage 已在运行，但窗口暂时无法唤起。请从任务栏打开 Cage。", "Cage"); }
            catch (TimeoutException) { MessageBox.Show("Cage 正在启动，请稍候。", "Cage"); }
            return 0;
        }
        using var form = new MainWindow();
        using var cancellation = new CancellationTokenSource();
        _ = Task.Run(async () =>
        {
            while (!cancellation.IsCancellationRequested)
            {
                try
                {
                    await using var pipe = new NamedPipeServerStream("Cage-" + key, PipeDirection.In, 1,
                        PipeTransmissionMode.Byte, PipeOptions.Asynchronous | PipeOptions.CurrentUserOnly);
                    await pipe.WaitForConnectionAsync(cancellation.Token);
                    var data = new byte[1];
                    if (await pipe.ReadAsync(data, cancellation.Token) == 1 && data[0] == 1 && form.IsHandleCreated)
                        form.BeginInvoke(form.RestoreWindow);
                }
                catch (OperationCanceledException) { break; }
                catch (IOException) { }
            }
        });
        Application.Run(form); cancellation.Cancel();
        return SmokeOutput == null || File.Exists(SmokeOutput) ? 0 : 1;
    }
}
