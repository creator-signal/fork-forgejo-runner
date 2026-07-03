package container

// Platform identifies an OS/architecture a back-end runs. Field names mirror
// the OCI image-spec so it maps cleanly to an image platform; Variant is
// best-effort (empty when the back-end cannot report it).
type Platform struct {
	OS           string
	Architecture string
	Variant      string
}
