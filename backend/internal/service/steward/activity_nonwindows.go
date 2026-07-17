//go:build !windows

package steward

import "context"

func (s *Service) collectForegroundActivity(context.Context, map[string]any) error { return nil }
