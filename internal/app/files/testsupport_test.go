package files

import "time"

func timeUnix(s int64) time.Time { return time.Unix(s, 0) }
