//go:build !windows

package speech

import "os"

func killWithParent(*os.Process) func() { return func() {} }
