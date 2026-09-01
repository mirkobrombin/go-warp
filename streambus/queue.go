package streambus

type ringQueue struct {
	items []Frame
	head  int
	len   int
}

func newRingQueue(capacity int) ringQueue {
	return ringQueue{items: make([]Frame, capacity)}
}

func (q *ringQueue) push(frame Frame) {
	idx := (q.head + q.len) % len(q.items)
	q.items[idx] = frame
	q.len++
}

func (q *ringQueue) peek() (Frame, bool) {
	if q.len == 0 {
		return Frame{}, false
	}
	return q.items[q.head], true
}

func (q *ringQueue) pop() (Frame, bool) {
	if q.len == 0 {
		return Frame{}, false
	}
	frame := q.items[q.head]
	q.items[q.head] = Frame{}
	q.head = (q.head + 1) % len(q.items)
	q.len--
	return frame, true
}

type frameQueue struct {
	queues   [4]ringQueue
	capacity int
	len      int
}

func newFrameQueue(capacity int) frameQueue {
	queue := frameQueue{capacity: capacity}
	for i := range queue.queues {
		queue.queues[i] = newRingQueue(capacity)
	}
	return queue
}

func (q *frameQueue) push(frame Frame) bool {
	if q.len == q.capacity {
		return false
	}
	priority := int(frame.Priority)
	if priority < int(PriorityBulk) || priority > int(PriorityCritical) {
		priority = int(PriorityNormal)
	}
	q.queues[priority].push(frame)
	q.len++
	return true
}

// pop returns the highest-priority queued frame while retaining FIFO order
// within a priority class.
func (q *frameQueue) pop() (Frame, bool) {
	for priority := int(PriorityCritical); priority >= int(PriorityBulk); priority-- {
		if frame, ok := q.queues[priority].pop(); ok {
			q.len--
			return frame, true
		}
	}
	return Frame{}, false
}

// popOldest removes the earliest sequence regardless of priority. It is used
// by DropOldest, whose name describes age rather than scheduling priority.
func (q *frameQueue) popOldest() (Frame, bool) {
	selected := -1
	var oldest Frame
	for priority := int(PriorityBulk); priority <= int(PriorityCritical); priority++ {
		frame, ok := q.queues[priority].peek()
		if !ok {
			continue
		}
		if selected == -1 || frame.Sequence < oldest.Sequence {
			selected = priority
			oldest = frame
		}
	}
	if selected == -1 {
		return Frame{}, false
	}
	frame, _ := q.queues[selected].pop()
	q.len--
	return frame, true
}

func (q *frameQueue) clear() int {
	dropped := q.len
	for priority := range q.queues {
		for q.queues[priority].len > 0 {
			_, _ = q.queues[priority].pop()
		}
	}
	q.len = 0
	return dropped
}
