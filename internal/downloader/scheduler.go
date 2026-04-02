package downloader

func (rdi *RangeDownloadInfo) pickTaskForWorker(worker *WorkerInfo) (task *ChunkTask, ok bool) {
	popNormal := func() (*ChunkTask, bool) {
		for {
			ct, ok := <-rdi.NormalQueue
			if !ok {
				return nil, false
			}
			if ct.isUsable() {
				worker.IsHedging = false
				return ct, true
			}
		}
	}

	popHedged := func() (*ChunkTask, bool) {
		if worker.Status != WorkerStatusIdle {
			return nil, false
		}

		for _, ct := range rdi.Chunks {
			if ct.isHedgeable() && ct.tryAcquireHedgeSlot() {
				worker.IsHedging = true
				return ct, true
			}
		}
		return nil, false
	}

	if t, ok := popNormal(); ok {
		return t, true
	}

	if t, ok := popHedged(); ok {
		return t, true
	}

	return nil, false
}
