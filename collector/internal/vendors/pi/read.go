package pi

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

func openRegularInput(ctx context.Context, path string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: Pi input must be a regular file", vendors.ErrInvalidData)
	}
	// Nonblocking open closes the stat/open FIFO replacement window on Unix.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err = file.Stat()
	if contextErr := ctx.Err(); contextErr != nil {
		file.Close()
		return nil, contextErr
	}
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: Pi input must be a regular file", vendors.ErrInvalidData)
	}
	return file, nil
}
