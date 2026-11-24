#!/usr/bin/perl
# SPDX-License-Identifier: AGPL-3.0-only
# Sends one HEP version 3 packet carrying a SIP message, the way a capture
# agent would, and prints what comes back within two seconds (nothing: no
# output). Used to check that the SIP core does not act on HEP it receives
# (T-51): the packet claims to come from <claimed source>, which may be a
# trunk's address.
#   hep-send.pl <to address:port> <claimed source address:port> <claimed destination address:port> <file with the SIP message>
use strict;
use warnings;
use IO::Socket::INET;
use Socket qw(inet_aton);

my ($to, $src, $dst, $file) = @ARGV;
die "usage: $0 <to> <claimed source> <claimed destination> <message file>\n" unless $file;
my ($src_ip, $src_port) = split /:/, $src;
my ($dst_ip, $dst_port) = split /:/, $dst;
open my $fh, '<', $file or die "$file: $!\n";
my $sip = do { local $/; <$fh> };
close $fh;
$sip =~ s/\r?\n/\r\n/g; # SIP lines end in CRLF whatever the file has
my ($head, $sdp) = split /\r\n\r\n/, $sip, 2;
$sdp //= '';
$head =~ s/^Content-Length:.*$/Content-Length: @{[length $sdp]}/mi;
$sip = "$head\r\n\r\n$sdp";

# A chunk: vendor 0 (generic), type, length including its own six bytes.
sub chunk { my ($type, $value) = @_; pack('nnn', 0, $type, 6 + length $value) . $value }
my $body = chunk(1, pack('C', 2))           # IPv4
  . chunk(2, pack('C', 17))                 # UDP
  . chunk(3, inet_aton($src_ip))
  . chunk(4, inet_aton($dst_ip))
  . chunk(7, pack('n', $src_port))
  . chunk(8, pack('n', $dst_port))
  . chunk(9, pack('N', time))
  . chunk(10, pack('N', 0))
  . chunk(11, pack('C', 1))                 # SIP
  . chunk(12, pack('N', 2001))              # capture agent id
  . chunk(15, $sip);
my $packet = 'HEP3' . pack('n', 6 + length $body) . $body;

my $sock = IO::Socket::INET->new(Proto => 'udp', PeerAddr => $to, ($ENV{HEP_FROM} ? (LocalAddr => $ENV{HEP_FROM}) : ())) or die "socket: $!\n";
$sock->send($packet) or die "send: $!\n";
my $rin = '';
vec($rin, fileno($sock), 1) = 1;
while (select(my $rout = $rin, undef, undef, 2)) {
  my $got;
  last unless defined $sock->recv($got, 65535);
  print "$got\n";
}
