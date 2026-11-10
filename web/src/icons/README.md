# Kubernetes icons (vendored)

These SVGs are copied unmodified from the Kubernetes Icons Set in
[kubernetes/community](https://github.com/kubernetes/community/tree/master/icons), commit
`307ae50e8e4246de172f195c73e3f026b4f0c7ff`:

- `svg/resources/unlabeled/`: deploy, sts, ds, job, cronjob, rs, pod, svc, cm, hpa, ing, netpol, pvc, ep
- `svg/infrastructure_components/unlabeled/`: node, control-plane, etcd
- `svg/control_plane_components/labeled/`: api, sched, c-m (no unlabeled variant exists upstream)

## Licence

The icons README states: "The Kubernetes Icons Set is licensed under a choice of either
Apache-2.0 or CC-BY-4.0 (Creative Commons Attribution 4.0 International)." This project uses
them under **Apache-2.0**; `LICENSE` is the Apache-2.0 text from the same commit of
kubernetes/community. Copyright the Kubernetes Authors.

## Changes

The files on disk are unchanged. `sprite.tsx` builds the page's `<symbol>` sprite from them at
load time and drops Inkscape metadata and the `<text>` labels of the three control-plane icons
(the page draws its own labels at 12px or larger, PRD R10).

The Kubernetes logo is a registered trademark of The Linux Foundation; these icons are used
only to identify Kubernetes object kinds.
