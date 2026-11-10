using System.Diagnostics;
using System.Globalization;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace Cage.GuestClipboard;

// Only the paired host can apply these bounded platform settings. No command,
// registry path, executable, account or file name is accepted from the wire.
internal static class WindowsSystemSettings
{
    internal static async Task Apply(JsonObject settings,Guid vm,string sid,CancellationToken token)
    {
        if(settings.Count is not (3 or 4) || settings["action"]?.GetValue<string>()!="apply")throw new InvalidDataException("settings_invalid");
        var iana=settings["timezone"]?.GetValue<string>() ?? "";var locale=settings["locale"]?.GetValue<string>() ?? "";
        if(iana.Length>128 || !TimeZoneInfo.TryConvertIanaIdToWindowsId(iana,out var windowsZone) ||
            !Regex.IsMatch(windowsZone!,@"^[A-Za-z0-9 ()._+-]+$") || !Regex.IsMatch(locale,@"^[a-zA-Z]{2,3}_[a-zA-Z]{2,3}\.UTF-8$") || !Regex.IsMatch(sid,@"^S-1-5-21-\d+-\d+-\d+-\d+$"))throw new InvalidDataException("settings_invalid");
        var tag=locale[..^6].Replace('_','-');var culture=CultureInfo.GetCultureInfo(tag);
        _=TimeZoneInfo.FindSystemTimeZoneById(windowsZone!);
        var configPath=Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles),"Cage Guest Bridge",vm.ToString("D"),"guest-config.json");
        var previous=ProtectedConfig(configPath);
        var config=JsonNode.Parse(previous) as JsonObject ?? throw new InvalidDataException("settings_config_invalid");
        if(config["network_mode"]?.GetValue<string>()!="packet")throw new InvalidDataException("settings_config_invalid");
        // Existing v1 controllers omit browser_language. Preserve the protected
        // selection during a component update; never derive it from locale.
        var language=settings.Count==4 ? BrowserLanguage(settings) : BrowserLanguage(new JsonObject{["browser_language"]=config["language"]?.DeepClone()});
        config["timezone"]=iana;config["language"]=language;
        // Publish before applying the OS settings. A failed OS transaction
        // restores the protected browser configuration as well.
        PublishConfig(configPath,System.Text.Encoding.UTF8.GetBytes(config.ToJsonString()));
        var script=$"$ErrorActionPreference='Stop';$key='Registry::HKEY_USERS\\{sid}\\Control Panel\\International';if(!(Test-Path -LiteralPath $key)){{throw 'gui_profile_not_loaded'}};$before=Get-ItemProperty -LiteralPath $key;$zone=(Get-TimeZone).Id;$system=(Get-WinSystemLocale).Name;try{{Set-TimeZone -Id '{windowsZone}';Set-ItemProperty -LiteralPath $key -Name LocaleName -Value '{culture.Name}';Set-ItemProperty -LiteralPath $key -Name Locale -Value '{culture.LCID:X8}';Set-WinSystemLocale -SystemLocale '{culture.Name}'}}catch{{Set-TimeZone -Id $zone;Set-ItemProperty -LiteralPath $key -Name LocaleName -Value $before.LocaleName;Set-ItemProperty -LiteralPath $key -Name Locale -Value $before.Locale;Set-WinSystemLocale -SystemLocale $system;throw}}";
        var start=new ProcessStartInfo(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Windows),@"System32\WindowsPowerShell\v1.0\powershell.exe")){UseShellExecute=false,RedirectStandardInput=true,RedirectStandardOutput=true,RedirectStandardError=true,CreateNoWindow=true};
        foreach(var argument in new[]{"-NoLogo","-NoProfile","-NonInteractive","-Command","-"})start.ArgumentList.Add(argument);
        try
        {
            using var process=Process.Start(start) ?? throw new InvalidDataException("settings_apply_failed");
            using var cancel=token.Register(()=>{try{process.Kill(true);}catch{}});
            var output=process.StandardOutput.BaseStream.CopyToAsync(Stream.Null,token);var errors=process.StandardError.BaseStream.CopyToAsync(Stream.Null,token);
            await process.StandardInput.WriteLineAsync(script);process.StandardInput.Close();await process.WaitForExitAsync(token);await Task.WhenAll(output,errors);
            if(process.ExitCode!=0)throw new InvalidDataException("settings_apply_failed");
        }
        catch { PublishConfig(configPath,previous);throw; }
    }

    internal static string BrowserLanguage(JsonObject settings)
    {
        var language=settings["browser_language"]?.GetValue<string>() ?? "";
        if(language.Length>128 || !Regex.IsMatch(language,@"^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$"))throw new InvalidDataException("settings_invalid");
        return language;
    }

    private static byte[] ProtectedConfig(string path)
    {
        var file=new FileInfo(path);
        for(var directory=file.Directory;directory!=null;directory=directory.Parent)
            if(directory.Attributes.HasFlag(FileAttributes.ReparsePoint))throw new InvalidDataException("settings_config_invalid");
        if(!file.Exists || file.Length is <=0 or >16384 || file.Attributes.HasFlag(FileAttributes.ReparsePoint))throw new InvalidDataException("settings_config_invalid");
        var acl=file.GetAccessControl();
        if(acl.GetOwner(typeof(SecurityIdentifier))?.Value is not ("S-1-5-18" or "S-1-5-32-544"))throw new InvalidDataException("settings_config_invalid");
        const FileSystemRights writes=FileSystemRights.WriteData|FileSystemRights.AppendData|FileSystemRights.WriteAttributes|FileSystemRights.WriteExtendedAttributes|FileSystemRights.Delete|FileSystemRights.ChangePermissions|FileSystemRights.TakeOwnership;
        foreach(FileSystemAccessRule rule in acl.GetAccessRules(true,true,typeof(SecurityIdentifier)))
            if(rule.AccessControlType==AccessControlType.Allow && (rule.FileSystemRights&writes)!=0 && rule.IdentityReference.Value is not ("S-1-5-18" or "S-1-5-32-544"))throw new InvalidDataException("settings_config_invalid");
        using var snapshot=new FileStream(path,FileMode.Open,FileAccess.Read,FileShare.Read|FileShare.Delete);
        if(snapshot.Length is <=0 or >16384)throw new InvalidDataException("settings_config_invalid");
        using var data=new MemoryStream();snapshot.CopyTo(data);return data.ToArray();
    }
    private static void PublishConfig(string path,byte[] data)
    {
        var stage=path+"."+Guid.NewGuid().ToString("N")+".part";
        try
        {
            File.WriteAllBytes(stage,data);
            new FileInfo(stage).SetAccessControl(new FileInfo(path).GetAccessControl());
            WindowsFilePublication.Replace(stage,path,default).GetAwaiter().GetResult();
        }
        finally { if(File.Exists(stage))File.Delete(stage); }
    }
}
