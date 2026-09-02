//go:build !embedded_helpers

package remote

func newProductionHelperReleaseProvider() (HelperReleaseProvider, bool) {
	return unavailableReleaseProvider{}, false
}
