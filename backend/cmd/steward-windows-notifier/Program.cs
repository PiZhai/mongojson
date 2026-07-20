using System.IO.Pipes;
using System.Runtime.InteropServices;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using Microsoft.Windows.AppNotifications;
using Microsoft.Windows.AppNotifications.Builder;

static string? Argument(string[] values, string name)
{
    for (var index = 0; index + 1 < values.Length; index++)
    {
        if (string.Equals(values[index], name, StringComparison.OrdinalIgnoreCase))
        {
            return values[index + 1];
        }
    }
    return null;
}

static Dictionary<string, string> ParseActivationArguments(string value)
{
    var result = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
    foreach (var pair in (value ?? string.Empty).Split('&', StringSplitOptions.RemoveEmptyEntries))
    {
        var separator = pair.IndexOf('=');
        var key = separator < 0 ? pair : pair[..separator];
        var item = separator < 0 ? string.Empty : pair[(separator + 1)..];
        result[Uri.UnescapeDataString(key.Replace('+', ' '))] = Uri.UnescapeDataString(item.Replace('+', ' '));
    }
    return result;
}

static async Task<string> ReadNewlineFrameAsync(Stream stream, CancellationToken cancellationToken)
{
    const int maximumFrameBytes = 64 * 1024;
    using var frame = new MemoryStream();
    var buffer = new byte[256];
    while (frame.Length <= maximumFrameBytes)
    {
        var count = await stream.ReadAsync(buffer.AsMemory(), cancellationToken);
        if (count == 0)
        {
            throw new EndOfStreamException("Named pipe response ended before a complete newline-delimited frame was received.");
        }
        var newline = Array.IndexOf(buffer, (byte)'\n', 0, count);
        if (newline >= 0)
        {
            frame.Write(buffer, 0, newline + 1);
            if (frame.Length > maximumFrameBytes)
            {
                break;
            }
            return Encoding.ASCII.GetString(frame.ToArray()).TrimEnd('\r', '\n');
        }
        frame.Write(buffer, 0, count);
    }
    throw new InvalidDataException($"Named pipe response frame exceeded {maximumFrameBytes} bytes.");
}

static async Task<int> RunProtocolSelfTestsAsync()
{
    await using (var fragmented = new ChunkedReadStream(
        Encoding.ASCII.GetBytes("HTTP/1.1 20"),
        Encoding.ASCII.GetBytes("2 Accepted\r"),
        Encoding.ASCII.GetBytes("\nContent-Length: 0\r\n\r\n")))
    {
        var status = await ReadNewlineFrameAsync(fragmented, CancellationToken.None);
        if (!string.Equals(status, "HTTP/1.1 202 Accepted", StringComparison.Ordinal))
        {
            Console.Error.WriteLine($"fragmented frame test failed: {status}");
            return 1;
        }
    }
    try
    {
        await using var incomplete = new ChunkedReadStream(Encoding.ASCII.GetBytes("HTTP/1.1 202 Accepted"));
        _ = await ReadNewlineFrameAsync(incomplete, CancellationToken.None);
        Console.Error.WriteLine("incomplete frame test did not fail");
        return 1;
    }
    catch (EndOfStreamException)
    {
        // Expected: an EOF without a newline is not a complete protocol frame.
    }
    var temporaryDirectory = Path.Combine(Path.GetTempPath(), "steward-notifier-self-test-" + Guid.NewGuid().ToString("N"));
    try
    {
        var queued = new PendingFeedback("signed-token", "snoozed", "900", 900, DateTimeOffset.UtcNow);
        var path = await QueueFeedbackAsync(queued, temporaryDirectory);
        var restored = ReadQueuedFeedback(path);
        if (restored.CallbackToken != queued.CallbackToken || restored.Action != queued.Action || restored.SnoozeSeconds != queued.SnoozeSeconds)
        {
            Console.Error.WriteLine("encrypted callback outbox round-trip test failed");
            return 1;
        }
    }
    finally
    {
        if (Directory.Exists(temporaryDirectory))
        {
            Directory.Delete(temporaryDirectory, recursive: true);
        }
    }
    Console.WriteLine("steward-windows-notifier protocol self-tests passed");
    return 0;
}

static int ParseSnoozeSeconds(string? value)
{
    return int.TryParse(value, out var seconds) && seconds > 0 ? seconds : 0;
}

static async Task<bool> ForwardFeedbackAsync(string token, string action, string actionValue, int snoozeSeconds, TimeSpan? operationTimeout = null)
{
    if (string.IsNullOrWhiteSpace(token) || string.IsNullOrWhiteSpace(action))
    {
        return false;
    }
    using var pipe = new NamedPipeClientStream(".", "MongojsonStewardCompanion", PipeDirection.InOut, PipeOptions.Asynchronous);
    using var timeout = new CancellationTokenSource(operationTimeout ?? TimeSpan.FromSeconds(10));
    await pipe.ConnectAsync(timeout.Token);
    var payload = JsonSerializer.SerializeToUtf8Bytes(new
    {
        callback_token = token,
        action,
        occurred_at = DateTimeOffset.UtcNow,
        metadata = new { source = "windows_app_notification", action_value = actionValue, snooze_seconds = snoozeSeconds }
    });
    var headers = Encoding.ASCII.GetBytes(
        "POST /notification-feedback HTTP/1.1\r\n" +
        "Host: steward-companion\r\n" +
        "Content-Type: application/json\r\n" +
        $"Content-Length: {payload.Length}\r\n" +
        "Connection: close\r\n\r\n");
    await pipe.WriteAsync(headers, timeout.Token);
    await pipe.WriteAsync(payload, timeout.Token);
    await pipe.FlushAsync(timeout.Token);
    var statusLine = await ReadNewlineFrameAsync(pipe, timeout.Token);
    return statusLine.Contains(" 200 ", StringComparison.Ordinal) || statusLine.Contains(" 202 ", StringComparison.Ordinal);
}

static string FeedbackOutboxDirectory()
{
    return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
        "MongojsonSteward", "notification-feedback-outbox");
}

static void RestrictFeedbackDirectory(string directory)
{
    Directory.CreateDirectory(directory);
    var currentUser = WindowsIdentity.GetCurrent().User ?? throw new InvalidOperationException("Current Windows user SID is unavailable.");
    var system = new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null);
    var inheritance = InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit;
    var security = new DirectorySecurity();
    security.SetAccessRuleProtection(isProtected: true, preserveInheritance: false);
    security.AddAccessRule(new FileSystemAccessRule(currentUser, FileSystemRights.FullControl, inheritance, PropagationFlags.None, AccessControlType.Allow));
    security.AddAccessRule(new FileSystemAccessRule(system, FileSystemRights.FullControl, inheritance, PropagationFlags.None, AccessControlType.Allow));
    new DirectoryInfo(directory).SetAccessControl(security);
}

static async Task<string> QueueFeedbackAsync(PendingFeedback feedback, string? directory = null)
{
    directory ??= FeedbackOutboxDirectory();
    RestrictFeedbackDirectory(directory);
    var plaintext = JsonSerializer.SerializeToUtf8Bytes(feedback);
    var encrypted = CurrentUserDataProtection.Protect(plaintext);
    var destination = Path.Combine(directory, Guid.NewGuid().ToString("N") + ".bin");
    var temporary = destination + ".tmp";
    try
    {
        await File.WriteAllBytesAsync(temporary, encrypted);
        File.Move(temporary, destination);
        return destination;
    }
    finally
    {
        if (File.Exists(temporary))
        {
            File.Delete(temporary);
        }
    }
}

static PendingFeedback ReadQueuedFeedback(string path)
{
    var plaintext = CurrentUserDataProtection.Unprotect(File.ReadAllBytes(path));
    return JsonSerializer.Deserialize<PendingFeedback>(plaintext) ?? throw new InvalidDataException("Queued notification feedback is empty.");
}

static async Task FlushQueuedFeedbackAsync()
{
    var directory = FeedbackOutboxDirectory();
    if (!Directory.Exists(directory))
    {
        return;
    }
    RestrictFeedbackDirectory(directory);
    foreach (var path in Directory.EnumerateFiles(directory, "*.bin").OrderBy(value => value, StringComparer.Ordinal))
    {
        try
        {
            var feedback = ReadQueuedFeedback(path);
            if (feedback.EnqueuedAt < DateTimeOffset.UtcNow.AddDays(-3))
            {
                File.Delete(path);
                continue;
            }
            if (await ForwardFeedbackAsync(feedback.CallbackToken, feedback.Action, feedback.ActionValue, feedback.SnoozeSeconds, TimeSpan.FromSeconds(2)))
            {
                File.Delete(path);
            }
            else
            {
                break;
            }
        }
        catch (Exception exception)
        {
            Console.Error.WriteLine($"queued notification feedback retry failed for {path}: {exception.Message}");
            break;
        }
    }
}

static async Task<bool> ForwardOrQueueFeedbackAsync(string token, string action, string actionValue, int snoozeSeconds)
{
    try
    {
        if (await ForwardFeedbackAsync(token, action, actionValue, snoozeSeconds))
        {
            return true;
        }
    }
    catch (Exception exception)
    {
        Console.Error.WriteLine($"notification feedback pipe delivery failed; queuing locally: {exception.Message}");
    }
    try
    {
        _ = await QueueFeedbackAsync(new PendingFeedback(token, action, actionValue, snoozeSeconds, DateTimeOffset.UtcNow));
        return true;
    }
    catch (Exception exception)
    {
        Console.Error.WriteLine($"notification feedback local queue failed: {exception}");
        return false;
    }
}

static NotificationAction[] DecodeActions(string? encoded)
{
    if (string.IsNullOrWhiteSpace(encoded))
    {
        return [];
    }
    try
    {
        var data = Convert.FromBase64String(encoded);
        return JsonSerializer.Deserialize<NotificationAction[]>(data) ?? [];
    }
    catch
    {
        return [];
    }
}

if (args.Any(value => string.Equals(value, "--self-test", StringComparison.OrdinalIgnoreCase)))
{
    return await RunProtocolSelfTestsAsync();
}

var activation = new TaskCompletionSource<int>(TaskCreationOptions.RunContinuationsAsynchronously);
AppNotificationManager.Default.NotificationInvoked += async (_, invoked) =>
{
    try
    {
        var values = ParseActivationArguments(invoked.Argument);
        var actionValue = values.GetValueOrDefault("action_value") ?? string.Empty;
        var snoozeSeconds = ParseSnoozeSeconds(values.GetValueOrDefault("snooze_seconds"));
        var ok = values.TryGetValue("callback_token", out var token) &&
                 values.TryGetValue("action", out var action) &&
                 await ForwardOrQueueFeedbackAsync(token, action, actionValue, snoozeSeconds);
        activation.TrySetResult(ok ? 0 : 1);
    }
    catch (Exception exception)
    {
        Console.Error.WriteLine(exception.ToString());
        activation.TrySetResult(1);
    }
};

try
{
    // For an unpackaged Windows App SDK executable Register installs the COM
    // activation registration. Keep it registered after this short-lived send
    // process exits so a later toast click cold-launches this executable.
    AppNotificationManager.Default.Register();
}
catch (Exception exception)
{
    Console.Error.WriteLine(exception.ToString());
    return 1;
}

try
{
    await FlushQueuedFeedbackAsync();
}
catch (Exception exception)
{
    Console.Error.WriteLine($"queued notification feedback startup retry failed: {exception}");
}

var title = Argument(args, "--title");
var body = Argument(args, "--body");
if (string.IsNullOrWhiteSpace(title) || string.IsNullOrWhiteSpace(body))
{
    // A cold activation has no send arguments. Registering above dispatches
    // NotificationInvoked; wait only long enough to durably hand feedback to
    // the Companion's encrypted outbox.
    var completed = await Task.WhenAny(activation.Task, Task.Delay(TimeSpan.FromSeconds(15)));
    return completed == activation.Task ? await activation.Task : 1;
}

var id = Argument(args, "--id") ?? Guid.NewGuid().ToString("D");
try
{
    var builder = new AppNotificationBuilder()
        .AddArgument("notification_id", id)
        .AddText(title)
        .AddText(body);
    foreach (var action in DecodeActions(Argument(args, "--actions-base64")))
    {
        if (string.IsNullOrWhiteSpace(action.Label) || string.IsNullOrWhiteSpace(action.CallbackToken))
        {
            continue;
        }
        var button = new AppNotificationButton(action.Label)
            .AddArgument("action", action.Kind)
            .AddArgument("callback_token", action.CallbackToken)
            .AddArgument("action_value", action.Value);
        var snoozeSeconds = ParseSnoozeSeconds(action.Value);
        if (snoozeSeconds > 0)
        {
            button.AddArgument("snooze_seconds", snoozeSeconds.ToString());
        }
        builder.AddButton(button);
    }
    var notification = builder.BuildNotification();
    notification.Tag = id.Length <= 16 ? id : id[..16];
    notification.Group = "steward";
    AppNotificationManager.Default.Show(notification);
    Console.WriteLine(id);
    return 0;
}
catch (Exception exception)
{
    Console.Error.WriteLine(exception.ToString());
    return 1;
}

internal sealed class NotificationAction
{
    [JsonPropertyName("id")]
    public string ID { get; set; } = string.Empty;

    [JsonPropertyName("label")]
    public string Label { get; set; } = string.Empty;

    [JsonPropertyName("kind")]
    public string Kind { get; set; } = string.Empty;

    [JsonPropertyName("value")]
    public string Value { get; set; } = string.Empty;

    [JsonPropertyName("callback_token")]
    public string CallbackToken { get; set; } = string.Empty;
}

internal sealed record PendingFeedback(
    string CallbackToken,
    string Action,
    string ActionValue,
    int SnoozeSeconds,
    DateTimeOffset EnqueuedAt);

internal static class CurrentUserDataProtection
{
    private const int CryptProtectUiForbidden = 0x1;

    public static byte[] Protect(byte[] plaintext) => Transform(plaintext, protect: true);
    public static byte[] Unprotect(byte[] ciphertext) => Transform(ciphertext, protect: false);

    private static byte[] Transform(byte[] input, bool protect)
    {
        if (input.Length == 0)
        {
            throw new ArgumentException("DPAPI input cannot be empty.", nameof(input));
        }
        var inputPointer = Marshal.AllocHGlobal(input.Length);
        try
        {
            Marshal.Copy(input, 0, inputPointer, input.Length);
            var inputBlob = new DataBlob { Size = input.Length, Data = inputPointer };
            DataBlob outputBlob;
            var ok = protect
                ? CryptProtectData(ref inputBlob, "MongojsonSteward notification feedback", IntPtr.Zero, IntPtr.Zero, IntPtr.Zero, CryptProtectUiForbidden, out outputBlob)
                : CryptUnprotectData(ref inputBlob, IntPtr.Zero, IntPtr.Zero, IntPtr.Zero, IntPtr.Zero, CryptProtectUiForbidden, out outputBlob);
            if (!ok)
            {
                throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error(), protect ? "DPAPI encryption failed." : "DPAPI decryption failed.");
            }
            try
            {
                var output = new byte[outputBlob.Size];
                Marshal.Copy(outputBlob.Data, output, 0, output.Length);
                return output;
            }
            finally
            {
                _ = LocalFree(outputBlob.Data);
            }
        }
        finally
        {
            Marshal.FreeHGlobal(inputPointer);
        }
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct DataBlob
    {
        public int Size;
        public IntPtr Data;
    }

    [DllImport("Crypt32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CryptProtectData(ref DataBlob dataIn, string? description, IntPtr optionalEntropy,
        IntPtr reserved, IntPtr prompt, int flags, out DataBlob dataOut);

    [DllImport("Crypt32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CryptUnprotectData(ref DataBlob dataIn, IntPtr description, IntPtr optionalEntropy,
        IntPtr reserved, IntPtr prompt, int flags, out DataBlob dataOut);

    [DllImport("Kernel32.dll")]
    private static extern IntPtr LocalFree(IntPtr memory);
}

internal sealed class ChunkedReadStream(params byte[][] chunks) : Stream
{
    private int _index;

    public override bool CanRead => true;
    public override bool CanSeek => false;
    public override bool CanWrite => false;
    public override long Length => throw new NotSupportedException();
    public override long Position { get => throw new NotSupportedException(); set => throw new NotSupportedException(); }

    public override int Read(byte[] buffer, int offset, int count)
    {
        if (_index >= chunks.Length)
        {
            return 0;
        }
        var chunk = chunks[_index++];
        var copied = Math.Min(count, chunk.Length);
        Array.Copy(chunk, 0, buffer, offset, copied);
        if (copied != chunk.Length)
        {
            throw new InvalidOperationException("Test chunks must fit in the requested read buffer.");
        }
        return copied;
    }

    public override ValueTask<int> ReadAsync(Memory<byte> buffer, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        if (_index >= chunks.Length)
        {
            return ValueTask.FromResult(0);
        }
        var chunk = chunks[_index++];
        if (chunk.Length > buffer.Length)
        {
            throw new InvalidOperationException("Test chunks must fit in the requested read buffer.");
        }
        chunk.AsSpan().CopyTo(buffer.Span);
        return ValueTask.FromResult(chunk.Length);
    }

    public override void Flush() { }
    public override long Seek(long offset, SeekOrigin origin) => throw new NotSupportedException();
    public override void SetLength(long value) => throw new NotSupportedException();
    public override void Write(byte[] buffer, int offset, int count) => throw new NotSupportedException();
}
