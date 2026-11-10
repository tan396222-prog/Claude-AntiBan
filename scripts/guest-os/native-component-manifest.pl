#!/usr/bin/perl
use strict;
use warnings;
use JSON::PP;
use Digest::SHA;
use Fcntl qw(:DEFAULT);

my ($mode, $manifest_path, $kit) = @ARGV;
die "Invalid component operation\n" unless defined($mode) && $mode =~ /^(source|targets|verify|publish)$/;
open(my $input, '<', $manifest_path) or die "Component manifest unavailable\n";
local $/;
my $encoded = <$input>;
close($input);
die "Component manifest too large\n" if length($encoded) > 4 * 1024 * 1024;
my $manifest = decode_json($encoded);
die "Invalid component manifest\n" unless $manifest->{protocol} == 1 && $manifest->{system} eq 'macos' && ref($manifest->{files}) eq 'ARRAY' && $manifest->{revision} =~ /^[a-f0-9]{64}$/;
for my $entry (@{$manifest->{files}}) {
    my ($source, $target) = ($entry->{source}, $entry->{target} // '');
    die "Invalid component source\n" unless defined($source) && $source !~ m{(?:^/|(?:^|/)\.\.?(?:/|$)|[\x00\r\n])};
    die "Invalid component target\n" if $target ne '' && ($target !~ m{^/(?:Applications/Cage[^/]*\.app/|usr/local/libexec/cage-)} || $target =~ m{(?:^|/)\.\.?(?:/|$)|[\x00\r\n]});
    die "Invalid component digest\n" unless $entry->{sha256} =~ /^[a-f0-9]{64}$/ && $entry->{installed_sha256} =~ /^[a-f0-9]{64}$/;
    if ($mode eq 'targets') { print "$source\0$target\0" if $target ne ''; next; }
    next if $mode ne 'source' && $target eq '';
    my $path = $mode eq 'source' ? "$kit/$source" : $target;
    open(my $file, '<', $path) or die "Missing component $path\n";
    binmode($file);
    my $actual = Digest::SHA->new(256)->addfile($file)->hexdigest;
    close($file);
    my $expected = $mode eq 'source' ? $entry->{sha256} : $entry->{installed_sha256};
    die "Component digest mismatch $path\n" unless $actual eq $expected;
}
if ($mode eq 'publish') {
    die "Component publication requires administrator\n" unless $> == 0;
    my $target = '/var/db/cage-components.json';
    die "Invalid existing component receipt\n" if -l $target || (-e $target && !-f $target);
    my $temporary = "$target.$$";
    sysopen(my $output, $temporary, O_WRONLY | O_CREAT | O_EXCL, 0644) or die "Cannot stage component receipt\n";
    print $output $encoded or die "Cannot write component receipt\n";
    close($output) or die "Cannot close component receipt\n";
    rename($temporary, $target) or die "Cannot publish component receipt\n";
}
