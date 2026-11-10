// Observe the guest kernel's IPv6 route selection for the fixed public probe.
// An interface-scoped default on Darwin is selectable by a process that
// requests IPV6_BOUND_IF, even when an ordinary route lookup returns no route.
#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <ifaddrs.h>
#include <net/if.h>
#include <netinet/in.h>
#include <poll.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

static int unicastSource(const struct in6_addr *address) {
    return !IN6_IS_ADDR_UNSPECIFIED(address) && !IN6_IS_ADDR_LOOPBACK(address)
        && !IN6_IS_ADDR_LINKLOCAL(address) && !IN6_IS_ADDR_MULTICAST(address);
}

// A link-local, one-hop datagram can leave the VM NIC even when the guest
// deliberately has no ordinary IPv6 default route. It never targets the internet.
static int sendProbe(const char *name, const char *run, const char *check) {
    if (strlen(run)!=32 || strlen(check)!=32 || strspn(run,"0123456789abcdef")!=32 || strspn(check,"0123456789abcdef")!=32) return 2;
    unsigned index=if_nametoindex(name); if(!index) return 1;
    int fd=socket(AF_INET6,SOCK_DGRAM,0), hops=1;
    if(fd<0 || setsockopt(fd,IPPROTO_IPV6,IPV6_BOUND_IF,&index,sizeof(index))!=0 ||
        setsockopt(fd,IPPROTO_IPV6,IPV6_MULTICAST_IF,&index,sizeof(index))!=0 ||
        setsockopt(fd,IPPROTO_IPV6,IPV6_MULTICAST_HOPS,&hops,sizeof(hops))!=0) return 1;
    struct sockaddr_in6 dst={.sin6_family=AF_INET6,.sin6_len=sizeof(dst),.sin6_scope_id=index,.sin6_port=htons(49417)};
    if(inet_pton(AF_INET6,"ff02::2",&dst.sin6_addr)!=1) return 1;
    char payload[120];int len=snprintf(payload,sizeof(payload),"Cage IPv6 link probe v1 %s %s",run,check);
    ssize_t sent=sendto(fd,payload,(size_t)len,0,(struct sockaddr *)&dst,sizeof(dst));
    close(fd); if(sent!=len) return 1; puts("sent");return 0;
}

int main(int argc, char **argv) {
    if (argc==5 && strcmp(argv[1],"link")==0) return sendProbe(argv[2],argv[3],argv[4]);
    if (argc != 2) return 2;
    int bound = strcmp(argv[1], "default") != 0;
    unsigned index = bound ? if_nametoindex(argv[1]) : 0;
    if (bound && !index) return 2;
    struct ifaddrs *interfaces;
    if (getifaddrs(&interfaces) != 0) return 1;
    int sources = 0, found = !bound;
    for (struct ifaddrs *item = interfaces; item; item = item->ifa_next) {
        if (bound && strcmp(item->ifa_name, argv[1])) continue;
        found = 1;
        if (item->ifa_addr && item->ifa_addr->sa_family == AF_INET6 && (item->ifa_flags & IFF_UP)) {
            struct sockaddr_in6 *a = (struct sockaddr_in6 *)item->ifa_addr;
            if (unicastSource(&a->sin6_addr)) sources++;
        }
    }
    freeifaddrs(interfaces);
    if (!found) return 1;
    int fd = socket(AF_INET6, SOCK_STREAM, 0);
    if (fd < 0 || fcntl(fd, F_SETFL, O_NONBLOCK) < 0) return 1;
    if (bound && setsockopt(fd, IPPROTO_IPV6, IPV6_BOUND_IF, &index, sizeof(index)) != 0) return 1;
    struct sockaddr_in6 dst = {.sin6_len = sizeof(dst), .sin6_family = AF_INET6, .sin6_port = htons(443)};
    if (inet_pton(AF_INET6, "2606:4700:4700::1111", &dst.sin6_addr) != 1) return 1;
    int error = connect(fd, (struct sockaddr *)&dst, sizeof(dst)) == 0 ? 0 : errno;
    if (error == EINPROGRESS) {
        struct pollfd pfd = {fd, POLLOUT, 0};
        int ready = poll(&pfd, 1, 1500);
        if (ready == 0) error = ETIMEDOUT;
        else if (ready < 0) return 1;
        else {
            socklen_t len = sizeof(error);
            if (getsockopt(fd, SOL_SOCKET, SO_ERROR, &error, &len) != 0) return 1;
        }
    }
    close(fd);
    const char *status = error == 0 ? "connected"
        : (error == ENETUNREACH || error == EHOSTUNREACH || error == EADDRNOTAVAIL) ? "no-route"
        : error == ETIMEDOUT ? "timeout" : "unknown";
    printf("{\"globalSources\":%d,\"boundIndex\":%u,\"result\":\"%s\",\"errno\":%d}\n", sources, index, status, error);
    return 0;
}
