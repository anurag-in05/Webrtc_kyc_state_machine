package recorder

// Test-only handles for cmd/videotest. The real driver is session.go; these
// just let an external test binary exercise the writer directly. Not used in
// production.
type VideoWriterTest struct{ w *videoWriter }
func NewVideoWriterForTest(path string) *VideoWriterTest {
	return &VideoWriterTest{w: newVideoWriter(path)}
}
func (t *VideoWriterTest) Send(tsUS uint64, au []byte) { t.w.send(tsUS, au) }
func (t *VideoWriterTest) Close()                      { t.w.close() }