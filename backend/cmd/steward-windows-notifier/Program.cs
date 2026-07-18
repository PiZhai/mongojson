using Microsoft.Windows.AppNotifications;
using Microsoft.Windows.AppNotifications.Builder;

static string? Argument(string[] args, string name)
{
    for (var index = 0; index + 1 < args.Length; index++)
    {
        if (string.Equals(args[index], name, StringComparison.OrdinalIgnoreCase))
        {
            return args[index + 1];
        }
    }
    return null;
}

var id = Argument(args, "--id") ?? Guid.NewGuid().ToString("D");
var title = Argument(args, "--title");
var body = Argument(args, "--body");
if (string.IsNullOrWhiteSpace(title) || string.IsNullOrWhiteSpace(body))
{
    Console.Error.WriteLine("--title and --body are required");
    return 2;
}

try
{
    AppNotificationManager.Default.Register();
    var notification = new AppNotificationBuilder()
        .AddArgument("notification_id", id)
        .AddText(title)
        .AddText(body)
        .BuildNotification();
    notification.Tag = id.Length <= 16 ? id : id[..16];
    notification.Group = "steward";
    AppNotificationManager.Default.Show(notification);
    AppNotificationManager.Default.Unregister();
    Console.WriteLine(id);
    return 0;
}
catch (Exception exception)
{
    Console.Error.WriteLine(exception.ToString());
    return 1;
}
