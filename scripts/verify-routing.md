# Proving the router actually routes

`verify-routing.sh` builds a miniature internet out of network namespaces and
checks that a marked packet leaves through the hop and an unmarked one does not.

It exists because every other test in this package asserts on generated text,
and text that looks right is not the same as a kernel that behaves. The first
run of this script found that `mark` is a reserved word in the nftables grammar,
which every string test had happily accepted.

Run it on any machine with Docker:

    docker run --rm --privileged -v "$PWD/scripts:/work" alpine:3.20 sh /work/verify-routing.sh

Expected output: three packets at the hop and none at the ISP while the routing
rule is installed, and the reverse once it is removed.
