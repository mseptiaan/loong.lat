package platform

import "log/slog"

var discard = slog.New(slog.DiscardHandler)
