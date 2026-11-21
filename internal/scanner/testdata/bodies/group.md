Filed by the `lint` scanner. A box is ticked when a scan no longer reports that finding.

<!-- ard:scanner=lint group=pkg/a%20b -->

<!-- ard:findings -->
- [ ] unused var x <!-- ard:fp=lint:a:1 sev=low -->
- [x] unused var y <!-- ard:fp=lint:a:2 sev=medium -->
<!-- /ard:findings -->

<!-- ard:details -->
<details><summary>unused var x</summary>

</details>

<!-- /ard:details -->
