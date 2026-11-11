//go:build !tinygo && !lowmem

package bridge

import "context"

func (a *App) connectMoonraker(ctx context.Context) error { return a.moonrakerClient.Connect(ctx) }
func (a *App) monitorReusable(context.Context) error      { panic("unreachable") }
