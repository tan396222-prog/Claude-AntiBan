use strict;
use warnings;
use File::Temp qw(tempdir);
use POSIX qw(_exit WNOHANG);
use Socket qw(AF_UNIX SOCK_STREAM PF_UNSPEC);

# Two child processes connected only to one another. A local, read-only SFTP
# endpoint substitutes for the project transport and no host files are shared.
umask 077;
my $tmp = tempdir('cage-check-XXXXXXXX', DIR => '/private/var/tmp', CLEANUP => 0);
my $source = "$tmp/source";
my $mount = "$tmp/mount";
my @children;
my $attached = '';
sub mounted_line {
    open my $list, '-|', '/sbin/mount' or die "mount list";
    my @matches = grep { index($_, " on $mount (") >= 0 } <$list>;
    close $list or die "mount list";
    die "ambiguous mount" if @matches > 1;
    return $matches[0] // '';
}
sub spawn_peer {
    my ($side, $other, @cmd) = @_;
    my $pid = fork();
    die "fork" unless defined $pid;
    return $pid if $pid;
    close $other;
    open STDIN, '<&', $side or _exit(125);
    open STDOUT, '>&', $side or _exit(125);
    open STDERR, '>', '/dev/null' or _exit(125);
    close $side;
    exec @cmd or _exit(126);
}
my $ok = eval {
    local $SIG{TERM} = local $SIG{HUP} = local $SIG{INT} = sub { die "cancelled" };
    mkdir $source, 0700 or die "source";
    mkdir $mount, 0700 or die "target";
    socketpair(my $a,my $b,AF_UNIX,SOCK_STREAM,PF_UNSPEC) or die "socketpair";
    push @children, spawn_peer($a,$b,'/usr/libexec/sftp-server','-R','-d',$source);
    push @children, spawn_peer($b,$a,'/usr/local/libexec/cage-sshfs',":$source",$mount,'-f','-o','slave,ro,quiet,backend=fskit,nodev,nosuid,dir_cache=no');
    close $a; close $b;
    for (1..35) {
        $attached = mounted_line();
        last if $attached;
        last unless kill 0, $children[1];
        select undef,undef,undef,0.2;
    }
    my $user = getpwuid($<);
    my ($flags) = $attached =~ m{^macfuse://[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12} on \Q$mount\E \(macfuse, ([^\n]+)\)\s*$}i;
    my %options = map { $_ => 1 } split /, /, ($flags // '');
    die "unexpected mount" unless $options{'fskit'} && $options{'read-only'} && $options{"mounted by $user"};
    1;
};
# Never unmount a replacement or remove a mounted tree.
my $live = mounted_line();
if ($ok && $live && $live eq $attached) {
    $ok = 0 unless system('/sbin/umount','-f',$mount) == 0;
}
for my $pid (@children) {
    if (waitpid($pid,WNOHANG)==0) {kill 'TERM',$pid}
}
select undef,undef,undef,0.15;
for my $pid (@children) {
    if (waitpid($pid,WNOHANG)==0) {kill 'KILL',$pid;waitpid($pid,0)}
}
$ok = 0 if mounted_line();
$ok = 0 unless rmdir $mount;
$ok = 0 unless rmdir $source;
$ok = 0 unless rmdir $tmp;
exit($ok ? 0 : 1);
