using System.Buffers.Binary;
using System.Net;
using System.Net.Sockets;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Cage.GuestClipboard;

internal static class Protocol
{
    internal static readonly Guid Service = new("f4a4c1ce-3c9f-49f1-b7bf-82df6592c0fa");
    internal static readonly Guid Parent = new("a42e7cda-d03f-480c-9cc2-a4de20abb878");
    internal const int WireLimit = 30 << 20, PathLimit = 1 << 20, TextLimit = 2 << 20, PngLimit = 20 << 20;
    internal static readonly UTF8Encoding Utf8 = new(false, true);
    internal static JsonObject PublicHello(JsonObject hello) => new() { ["hello"] = hello };

    // Do not use StreamReader here: its read-ahead would consume archive frames
    // following a JSON handshake on the same bidirectional stream.
    internal static async Task<JsonObject?> Read(Stream input, int limit, CancellationToken token)
    {
        if (input is ReadAheadStream buffered)
        {
            var bytes = await buffered.Line(limit, token);
            if (bytes == null) return null;
            return JsonNode.Parse(Utf8.GetString(bytes)) as JsonObject ?? throw new InvalidDataException("message_invalid");
        }
        using var line = new MemoryStream();
        var one = new byte[1];
        while (true)
        {
            if (await input.ReadAsync(one, token) == 0)
            {
                if (line.Length == 0) return null;
                throw new EndOfStreamException();
            }
            if (one[0] == 10) break;
            if (line.Length >= limit) throw new InvalidDataException("message_limit");
            line.WriteByte(one[0]);
        }
        return JsonNode.Parse(Utf8.GetString(line.GetBuffer(), 0, (int)line.Length)) as JsonObject
            ?? throw new InvalidDataException("message_invalid");
    }

    internal static async Task Write(Stream output, JsonObject data, CancellationToken token)
    {
        var bytes = Utf8.GetBytes(data.ToJsonString());
        if (bytes.Length > WireLimit) throw new InvalidDataException("message_limit");
        await output.WriteAsync(bytes, token);
        await output.WriteAsync(new byte[] { 10 }, token);
        await output.FlushAsync(token);
    }

    internal static string[] Paths(JsonNode? value)
    {
        if (value is not JsonArray a || a.Count is < 1 or > 100) throw new InvalidDataException("selection_invalid");
        var paths = a.Select(x => x?.GetValue<string>() ?? "").ToArray();
        if (paths.Any(x => string.IsNullOrWhiteSpace(x) || x.Contains('\0') || !Path.IsPathFullyQualified(x)) ||
            Utf8.GetByteCount(a.ToJsonString()) > PathLimit)
            throw new InvalidDataException("selection_invalid");
        return paths;
    }

    internal static void Png(byte[] data)
    {
        ReadOnlySpan<byte> signature = [137, 80, 78, 71, 13, 10, 26, 10];
        if (data.Length is < 33 or > PngLimit || !data.AsSpan(0, 8).SequenceEqual(signature) ||
            !data.AsSpan(12, 4).SequenceEqual("IHDR"u8)) throw new InvalidDataException("image_invalid");
        var w = BinaryPrimitives.ReadUInt32BigEndian(data.AsSpan(16, 4));
        var h = BinaryPrimitives.ReadUInt32BigEndian(data.AsSpan(20, 4));
        if (w == 0 || h == 0 || (ulong)w * h > 40_000_000) throw new InvalidDataException("image_limit");
    }

    internal static string Proof(byte[] secret, string role, string nonce, Guid vm, string sid, string session, string operation, JsonNode? paths)
    {
        var content = new JsonArray(role, nonce, vm.ToString("D"), sid, session, operation, paths?.ToJsonString() ?? "").ToJsonString();
        return Convert.ToBase64String(HMACSHA256.HashData(secret, Utf8.GetBytes(content)));
    }

    internal static bool EqualProof(string expected, string? actual)
    {
        try { return CryptographicOperations.FixedTimeEquals(Convert.FromBase64String(expected), Convert.FromBase64String(actual ?? "")); }
        catch (FormatException) { return false; }
    }

    // Archives use bounded frames internally. The zero frame is followed by a
    // result object, so a closed connection is never mistaken for a complete tar.
    internal static async Task SendFrames(Stream input, Stream output, CancellationToken token)
    {
        var buffer = new byte[64 << 10]; var header = new byte[4];
        int n;
        while ((n = await input.ReadAsync(buffer, token)) != 0)
        {
            BinaryPrimitives.WriteUInt32LittleEndian(header, (uint)n);
            await output.WriteAsync(header, token); await output.WriteAsync(buffer.AsMemory(0, n), token);
        }
        Array.Clear(header); await output.WriteAsync(header, token); await output.FlushAsync(token);
    }

    internal static async Task ReceiveFrames(Stream input, Stream output, CancellationToken token)
    {
        var buffer = new byte[64 << 10]; var header = new byte[4];
        while (true)
        {
            await input.ReadExactlyAsync(header, token);
            var n = BinaryPrimitives.ReadUInt32LittleEndian(header);
            if (n == 0) break;
            if (n > buffer.Length) throw new InvalidDataException("frame_invalid");
            await input.ReadExactlyAsync(buffer.AsMemory(0, (int)n), token);
            await output.WriteAsync(buffer.AsMemory(0, (int)n), token);
        }
        await output.FlushAsync(token);
    }
}

internal sealed class HyperVEndPoint(Guid vm, Guid service) : EndPoint
{
    internal Guid VM { get; } = vm;
    internal Guid Service { get; } = service;
    public override AddressFamily AddressFamily => (AddressFamily)34;
    public override SocketAddress Serialize()
    {
        var address = new SocketAddress(AddressFamily, 36);
        var bytes = VM.ToByteArray().Concat(Service.ToByteArray()).ToArray();
        for (var i = 0; i < bytes.Length; i++) address[i + 4] = bytes[i];
        return address;
    }
    public override EndPoint Create(SocketAddress address)
    {
        if (address.Family != AddressFamily || address.Size != 36) throw new InvalidDataException("endpoint_invalid");
        var vmBytes = new byte[16]; var serviceBytes = new byte[16];
        for (var i = 0; i < 16; i++) { vmBytes[i] = address[i + 4]; serviceBytes[i] = address[i + 20]; }
        return new HyperVEndPoint(new Guid(vmBytes), new Guid(serviceBytes));
    }
    internal static Socket Socket() => new((AddressFamily)34, SocketType.Stream, (ProtocolType)1);
}
