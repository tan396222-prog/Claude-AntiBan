using System.Diagnostics;

namespace Cage.GuestClipboard;

// Callers own both paths and finish writing their protected stage first.
// Publish one complete snapshot while retaining the old reader and its ACL.
internal static class WindowsFilePublication
{
    internal static async Task Replace(string stage,string destination,CancellationToken token)
    {
        var elapsed=Stopwatch.StartNew();
        for(;;)
        {
            token.ThrowIfCancellationRequested();
            try
            {
                if(File.Exists(destination))File.Replace(stage,destination,null);
                else File.Move(stage,destination);
                return;
            }
            catch(Exception error) when(error is IOException or UnauthorizedAccessException)
            {
                var code=error.HResult&0xffff;
                if(code is not (5 or 32) || elapsed.Elapsed>=TimeSpan.FromSeconds(2))throw;
                await Task.Delay(25,token);
            }
        }
    }
}
