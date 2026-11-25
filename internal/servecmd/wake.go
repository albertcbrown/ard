package servecmd

// wakeLoops asks the running controller to tick the named loops soon. The
// controller is built after the loops and the Closer, so it is bound late;
// before that (and in tests without one) a wake does nothing.
func (w *wiring) wakeLoops(reason string, names ...string) {
	if c := w.ctl.Load(); c != nil {
		c.WakeLoops(reason, names...)
	}
}
