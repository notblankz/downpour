package downloader

func isUsableTask(ct *ChunkTask) bool {
	if ct == nil {
		return false
	}

	if ct.Done.Load() {
		return false
	}

	if ct.Ctx != nil {
		select {
		case <-ct.Ctx.Done():
			return false
		default:
		}
	}
	return true
}

func (rdi *RangeDownloadInfo) pickTaskForWorker() (task *ChunkTask, ok bool) {
	popNormal := func() (*ChunkTask, bool) {
		ct, ok := <-rdi.NormalQueue
		if !ok {
			return nil, false
		}
		if isUsableTask(ct) {
			return ct, true
		}
		return nil, false
	}

	if t, ok := popNormal(); ok {
		return t, true
	}
	return nil, false
}
