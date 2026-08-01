// Package platform provides host detection, default path profiles, FUSE
// unmount helpers, doctor probes, and Unix peer credentials for the
// mount-wrapper control plane.
//
// Path profiles use the mount-wrapper FHS layout (/var/lib/mount-wrapper, …),
// not the upstream tarmount-wsl product names. Hooks env and control escape
// hatches use the MOUNT_WRAPPER_* prefix only.
package platform
