package main

import (
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"
	"github.com/robotn/gohook"
	"image"
	"image/color"
	"math"
	"sync"
	"time"
)

type RingBuffer struct {
	data     []int64
	size     int
	capacity int
	head     int
	mutex    sync.RWMutex
}

func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{
		data:     make([]int64, capacity),
		capacity: capacity,
	}
}

func (rb *RingBuffer) Append(value int64) {
	rb.mutex.Lock()
	defer rb.mutex.Unlock()

	if rb.size < rb.capacity {
		rb.data[rb.size] = value
		rb.size++
	} else {
		rb.data[rb.head] = value
		rb.head = (rb.head + 1) % rb.capacity
	}
}

func (rb *RingBuffer) GetAll() []int64 {
	rb.mutex.RLock()
	defer rb.mutex.RUnlock()

	result := make([]int64, rb.size)
	for i := 0; i < rb.size; i++ {
		result[i] = rb.data[(rb.head+i)%rb.capacity]
	}
	return result
}

type APMTracker struct {
	actions        *RingBuffer
	startTime      time.Time
	peakAPM        int
	running        bool
	updateInterval time.Duration
	app            fyne.App
	window         fyne.Window
	isMiniView     bool
	miniWindow     fyne.Window
	currentAPMVar  binding.String
	peakAPMVar     binding.String
	avgAPMVar      binding.String
	graphImage     *canvas.Image
	mutex          sync.Mutex
}

func NewAPMTracker() *APMTracker {
	return &APMTracker{
		actions:        NewRingBuffer(3600),
		startTime:      time.Now(),
		peakAPM:        0,
		running:        true,
		updateInterval: 500 * time.Millisecond,
		currentAPMVar:  binding.NewString(),
		peakAPMVar:     binding.NewString(),
		avgAPMVar:      binding.NewString(),
	}
}

func (a *APMTracker) onAction() {
	a.actions.Append(time.Now().UnixNano() / int64(time.Millisecond))
}

func (a *APMTracker) inputLoop() {
	evChan := hook.Start()
	defer hook.End()

	for ev := range evChan {
		if ev.Kind == hook.KeyDown || ev.Kind == hook.MouseDown {
			a.onAction()
		}
	}
}

func (a *APMTracker) calculateCurrentAPM() int {
	minuteAgo := time.Now().Add(-time.Minute).UnixNano() / int64(time.Millisecond)
	actions := a.actions.GetAll()
	count := 0
	for i := len(actions) - 1; i >= 0; i-- {
		if actions[i] < minuteAgo {
			break
		}
		count++
	}
	return count
}

func (a *APMTracker) calculateAverageAPM() float64 {
	elapsedMinutes := time.Since(a.startTime).Minutes()
	return float64(len(a.actions.GetAll())) / elapsedMinutes
}

func (a *APMTracker) updateGraph() {
	width, height := 400, 300
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.White)
		}
	}

	now := time.Now().UnixNano() / int64(time.Millisecond)
	data := a.actions.GetAll()
	buckets := make([]int, 60)
	for _, t := range data {
		if now-t <= 60000 {
			buckets[(now-t)/1000]++
		}
	}

	maxCount := 0
	for _, count := range buckets {
		if count > maxCount {
			maxCount = count
		}
	}

	if maxCount > 0 {
		for i, count := range buckets {
			barHeight := int(float64(count) / float64(maxCount) * float64(height))
			x := width - (i+1)*6
			for y := height - 1; y >= height-barHeight; y-- {
				for dx := 0; dx < 5; dx++ {
					img.Set(x+dx, y, color.RGBA{0, 0, 255, 255})
				}
			}
		}
	}

	a.graphImage.Image = img
	a.graphImage.Refresh()
}

func (a *APMTracker) updateGUI() {
	if !a.running {
		return
	}
	currentAPM := a.calculateCurrentAPM()
	avgAPM := a.calculateAverageAPM()

	a.currentAPMVar.Set(fmt.Sprintf("Current APM: %d", currentAPM))
	a.miniWindow.Content().(*widget.Label).SetText(fmt.Sprintf("APM: %d", currentAPM))
	a.peakAPM = int(math.Max(float64(a.peakAPM), float64(currentAPM)))
	a.peakAPMVar.Set(fmt.Sprintf("Peak APM: %d", a.peakAPM))
	a.avgAPMVar.Set(fmt.Sprintf("Average APM: %.2f", avgAPM))

	a.updateGraph()

	time.AfterFunc(a.updateInterval, a.updateGUI)
}

func (a *APMTracker) setupGUI() {
	a.app = app.New()
	a.window = a.app.NewWindow("APM Tracker")
	a.window.Resize(fyne.NewSize(600, 400))

	currentAPMLabel := widget.NewLabelWithData(a.currentAPMVar)
	peakAPMLabel := widget.NewLabelWithData(a.peakAPMVar)
	avgAPMLabel := widget.NewLabelWithData(a.avgAPMVar)

	a.graphImage = &canvas.Image{}
	a.graphImage.FillMode = canvas.ImageFillOriginal
	a.graphImage.SetMinSize(fyne.NewSize(400, 300))

	mainFrame := container.NewVBox(
		currentAPMLabel,
		peakAPMLabel,
		avgAPMLabel,
		a.graphImage,
		widget.NewButton("Toggle Mini View", func() {
			a.toggleView()
		}),
	)

	a.window.SetContent(mainFrame)

	// Create mini-view window
	a.miniWindow = a.app.NewWindow("")
	a.miniWindow.SetContent(widget.NewLabel(""))
	a.miniWindow.Resize(fyne.NewSize(120, 30))
	a.miniWindow.SetFixedSize(true)
	a.miniWindow.Hide()

	a.window.SetOnClosed(func() {
		a.onClosing()
	})

	go a.inputLoop()
	go a.updateGUI()
}

func (a *APMTracker) toggleView() {
	if a.isMiniView {
		a.miniWindow.Hide()
		a.window.Show()
	} else {
		a.window.Hide()
		a.miniWindow.Show()
	}
	a.isMiniView = !a.isMiniView
}

func (a *APMTracker) onClosing() {
	a.running = false
	a.app.Quit()
}

func (a *APMTracker) Run() {
	a.setupGUI()
	a.window.ShowAndRun()
}

func main() {
	tracker := NewAPMTracker()
	tracker.Run()
}
