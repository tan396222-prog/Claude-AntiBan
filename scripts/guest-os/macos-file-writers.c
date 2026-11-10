#include <errno.h>
#include <libproc.h>
#include <stdlib.h>
#include <sys/file.h>
#include <sys/proc_info.h>
#include <sys/stat.h>

// Return 1 while the verified application tree holds a writable descriptor,
// 0 after those descriptors close, and -1 when the observation is incomplete.
int cage_file_has_writer(int root, const char *path) {
    struct stat target;
    if (lstat(path, &target) != 0 || !S_ISREG(target.st_mode)) return -1;
    int pids[256] = {root};
    int count = 1;
    for (int index = 0; index < count; index++) {
        int children[256];
        int child_count = proc_listchildpids(pids[index], children, sizeof(children));
        if (child_count < 0 || child_count >= 256) return -1;
        for (int child = 0; child < child_count; child++) {
            int seen = 0;
            for (int prior = 0; prior < count; prior++) seen |= pids[prior] == children[child];
            if (!seen && children[child] > 0) {
                if (count == 256) return -1;
                pids[count++] = children[child];
            }
        }
        int needed = proc_pidinfo(pids[index], PROC_PIDLISTFDS, 0, NULL, 0);
        if (needed <= 0) {
            if (errno == ESRCH) continue;
            return -1;
        }
        if (needed > 32768 * (int)sizeof(struct proc_fdinfo)) return -1;
        int capacity = needed + 128 * (int)sizeof(struct proc_fdinfo);
        struct proc_fdinfo *fds = malloc((size_t)capacity);
        if (!fds) return -1;
        int received = proc_pidinfo(pids[index], PROC_PIDLISTFDS, 0, fds, capacity);
        if (received <= 0 || received >= capacity) { free(fds); return -1; }
        for (int fd = 0; fd < received / (int)sizeof(*fds); fd++) {
            if (fds[fd].proc_fdtype != PROX_FDTYPE_VNODE) continue;
            struct vnode_fdinfo info;
            int size = proc_pidfdinfo(pids[index], fds[fd].proc_fd, PROC_PIDFDVNODEINFO, &info, sizeof(info));
            if (size != sizeof(info)) {
                if (errno == EBADF || errno == ESRCH || errno == ENOENT) continue;
                free(fds); return -1;
            }
            if (info.pvi.vi_stat.vst_dev == (uint32_t)target.st_dev && info.pvi.vi_stat.vst_ino == target.st_ino &&
                (info.pfi.fi_openflags & FWRITE) != 0) { free(fds); return 1; }
        }
        free(fds);
    }
    return 0;
}
