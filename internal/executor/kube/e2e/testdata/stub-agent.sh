#!/bin/sh
# hello mode: the stub agent the inner HappyPath runs in the ard image's sh.
# It writes hello.txt and the role file, then prints what an agent could leak:
# its environment, mounts, process list and the secret directories.
cd /work || exit 3
echo hello > hello.txt
mkdir -p .ard/7
printf -- '---\nrole: engineer\nstatus: done\nattempt: 1\n---\nstub\n' > .ard/7/worker-engineer.md
echo "== env"; env
echo "== mountinfo"; cat /proc/self/mountinfo
echo "== proc"; ls /proc
echo "== secrets"; ls -d /var/run/secrets /var/run/ard /var/run/secrets/kubernetes.io/serviceaccount 2>&1
echo "== done"
