// Guest-side terminal client. The signed-in helper owns the selected folder's
// security scope. It gives a login shell our new slave PTY, while SSH owns
// the outer terminal. The outer controlling terminal never changes owners.
#import <Foundation/Foundation.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <util.h>
#include <poll.h>
#include <signal.h>
#include <fcntl.h>
#include <errno.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>

static volatile sig_atomic_t signalWrite = -1;
static struct termios original;
static int changedTerminal;

static void restoreTerminal(void) {
    if (changedTerminal) tcsetattr(STDIN_FILENO, TCSANOW, &original);
}

static void signalHandler(int number) {
    int saved = errno;
    unsigned char value = number;
    if (signalWrite >= 0) (void)write(signalWrite, &value, 1);
    errno = saved;
}

static int writeAll(int fd, const void *data, size_t size) {
    const unsigned char *bytes = data;
    while (size) {
        ssize_t n = write(fd, bytes, size);
        if (n < 0 && errno == EINTR) continue;
        if (n <= 0) return -1;
        bytes += n;
        size -= (size_t)n;
    }
    return 0;
}

static int sendTerminal(int control, int slave, NSData *request) {
    struct iovec iov = {(void *)request.bytes, request.length};
    union { struct cmsghdr align; char bytes[CMSG_SPACE(sizeof(int))]; } rights = {0};
    struct msghdr msg = {0};
    msg.msg_iov = &iov;
    msg.msg_iovlen = 1;
    msg.msg_control = rights.bytes;
    msg.msg_controllen = sizeof(rights.bytes);
    struct cmsghdr *header = CMSG_FIRSTHDR(&msg);
    header->cmsg_level = SOL_SOCKET;
    header->cmsg_type = SCM_RIGHTS;
    header->cmsg_len = CMSG_LEN(sizeof(int));
    memcpy(CMSG_DATA(header), &slave, sizeof(slave));
    ssize_t n;
    do { n = sendmsg(control, &msg, 0); } while (n < 0 && errno == EINTR);
    if (n <= 0) return -1;
    return writeAll(control, (const unsigned char *)request.bytes + n, request.length - (size_t)n);
}

static int relay(int control, int master, int signals) {
    struct pollfd fds[] = {{STDIN_FILENO, POLLIN, 0}, {master, POLLIN, 0},
                           {signals, POLLIN, 0}, {control, POLLIN, 0}};
    char buffer[16384];
    for (;;) {
        if (poll(fds, 4, -1) < 0) {
            if (errno == EINTR) continue;
            return 1;
        }
        if (fds[2].revents) {
            ssize_t n = read(signals, buffer, sizeof(buffer));
            for (ssize_t j = 0; j < n; j++) {
                if (buffer[j] != SIGWINCH) return 0;
            }
            struct winsize size;
            if (ioctl(STDIN_FILENO, TIOCGWINSZ, &size) == 0)
                ioctl(master, TIOCSWINSZ, &size);
        }
        if (fds[1].revents) {
            ssize_t n = read(master, buffer, sizeof(buffer));
            if (n < 0 && errno == EINTR) continue;
            if (n <= 0) return (n == 0 || errno == EIO) ? 0 : 1;
            if (writeAll(STDOUT_FILENO, buffer, (size_t)n) < 0) return 1;
            // The shell can exit just after printing. Drain the PTY before
            // treating an accompanying control-socket close as terminal EOF.
            if (fds[3].revents) continue;
        }
        if (fds[3].revents) return 0;
        if (fds[0].revents) {
            ssize_t n = read(STDIN_FILENO, buffer, sizeof(buffer));
            if (n < 0 && errno == EINTR) continue;
            if (n <= 0) return 0;
            if (writeAll(master, buffer, (size_t)n) < 0) return 1;
        }
    }
}

int main(int argc, char **argv) {
    @autoreleasepool {
        BOOL editor = argc == 3 && strcmp(argv[1], "--editor") == 0;
        BOOL claude = argc == 4 && strcmp(argv[1], "--claude") == 0;
        BOOL interactive = isatty(STDIN_FILENO) && isatty(STDOUT_FILENO);
        if ((argc != 2 && !editor && !claude) || (!editor && !interactive)) {
            fprintf(stderr, "A project directory and an interactive guest terminal are required.\n");
            return 2;
        }
        NSString *project = [NSString stringWithUTF8String:argv[editor || claude ? 2 : 1]];
        if (!project || !project.isAbsolutePath) return 2;
        NSString *path = [NSHomeDirectory() stringByAppendingPathComponent:@"Library/Application Support/Cage/project-terminal.sock"];
        struct sockaddr_un addr = {.sun_family = AF_UNIX};
        if (strlcpy(addr.sun_path, path.fileSystemRepresentation, sizeof(addr.sun_path)) >= sizeof(addr.sun_path)) return 2;
        addr.sun_len = SUN_LEN(&addr);
        int control = socket(AF_UNIX, SOCK_STREAM, 0);
        if (control < 0 || connect(control, (struct sockaddr *)&addr, addr.sun_len) != 0) {
            fprintf(stderr, "The project helper is unavailable in this guest login session.\n");
            return 1;
        }
        uid_t peer; gid_t group;
        if (getpeereid(control, &peer, &group) < 0 || peer != geteuid()) {
            fprintf(stderr, "The project helper belongs to a different guest user.\n");
            return 1;
        }
        signal(SIGPIPE, SIG_IGN);
        int master, slave;
        struct winsize size = {.ws_row = 32, .ws_col = 100};
        if (interactive && (tcgetattr(STDIN_FILENO, &original) < 0 || ioctl(STDIN_FILENO, TIOCGWINSZ, &size) < 0)) return 1;
        // The guest browser launches its fixed editor over pipes. Only the
        // helper's child needs a controlling terminal in that case.
        if (openpty(&master, &slave, NULL, interactive ? &original : NULL, &size) != 0 || fchmod(slave, 0600) < 0) return 1;
        NSMutableDictionary *operation = [@{@"action": editor ? @"editor" : claude ? @"claude" : @"terminal", @"path": project} mutableCopy];
        if (claude) {
            NSString *launch = [NSString stringWithUTF8String:argv[3]];
            if (!launch || [launch lengthOfBytesUsingEncoding:NSUTF8StringEncoding] > 4096) return 2;
            operation[@"launch"] = launch;
        }
        NSMutableData *request = [[NSJSONSerialization dataWithJSONObject:operation options:0 error:nil] mutableCopy];
        [request appendBytes:"\n" length:1];
        if (sendTerminal(control, slave, request) < 0) return 1;
        close(slave);
        char reply[3];
        if (recv(control, reply, sizeof(reply), MSG_WAITALL) != sizeof(reply) || memcmp(reply, "OK\n", 3) != 0) {
            fprintf(stderr, "The guest user has not approved this mounted project.\n");
            return 1;
        }
        int signals[2];
        if (pipe(signals) < 0 || fcntl(signals[1], F_SETFL, O_NONBLOCK) < 0) return 1;
        signalWrite = signals[1];
        const int events[] = {SIGWINCH, SIGHUP, SIGINT, SIGTERM, SIGQUIT};
        struct sigaction action = {.sa_handler = signalHandler};
        sigemptyset(&action.sa_mask);
        for (size_t i = 0; i < sizeof(events)/sizeof(events[0]); i++) sigaction(events[i], &action, NULL);
        if (interactive) {
            struct termios raw = original;
            cfmakeraw(&raw);
            atexit(restoreTerminal);
            if (tcsetattr(STDIN_FILENO, TCSANOW, &raw) < 0) return 1;
            changedTerminal = 1;
        }
        if (ioctl(STDIN_FILENO, TIOCGWINSZ, &size) == 0) ioctl(master, TIOCSWINSZ, &size);
        int code = relay(control, master, signals[0]);
        close(master);
        close(control);
        return code;
    }
}
