package jam

import (
	"bytes"
	"encoding/json"
	"image"
	"math"
)

// The brake notices a jam going in circles: the model does what it has
// already done, and the device shows what it already showed after it. A
// model in that state does not get out by itself. It taps the same two
// buttons until the action limit, and each tap costs a full round of
// tokens.
//
// Doing one thing many times is not a circle while the screen keeps
// changing: ten taps on a counter's plus are ten new screens.

const (
	// brakeWarn is how deep in a circle the model is told so.
	brakeWarn = 4
	// brakeStop is how deep ends the prompt: the model must deliver its
	// result.
	brakeStop = 8
	// sameArg is how far a number may move and be the same argument. A
	// model aiming at one button lands a few points off each time.
	sameArg = 16.0
	// gridSide is how many cells a screen is compared in, each way.
	gridSide = 24
	// sameCell is how far a cell's brightness may move, of 255, and be
	// the same screen. A blinking caret moves it less; a new digit more.
	sameCell = 16
)

// passive tools look or wait. Looking again is not trying again.
var passive = map[string]bool{
	"browser_screenshot": true, "browser_sleep": true, "vibium_listen": true,
	"browser_get_url": true, "browser_map": true, "browser_a11y_tree": true,
	"browser_find": true, "browser_get_text": true, "browser_get_value": true,
}

type brakeSays int

const (
	brakeQuiet brakeSays = iota
	brakeWarns
	brakeStops
)

// brake follows one prompt.
type brake struct {
	tried  []*attempt
	depth  int
	warned bool
}

// attempt is one thing the model did, with every outcome it has had.
type attempt struct {
	tool string
	args map[string]any
	seen []outcome
}

// outcome is what followed an action: the screen as a grid of
// brightness, or the tool's error when it failed. An action that left
// neither has an outcome nobody knows, which is the same as no other.
type outcome struct {
	grid    []uint8
	failure string
}

func outcomeOf(picture []byte, failure string) outcome {
	if len(picture) > 0 {
		if img, _, err := image.Decode(bytes.NewReader(picture)); err == nil {
			return outcome{grid: brightness(img)}
		}
	}
	return outcome{failure: failure}
}

// brightness averages a picture's luma over gridSide by gridSide cells.
func brightness(img image.Image) []uint8 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < gridSide || h < gridSide {
		return nil
	}
	// A few samples a cell each way are enough for an average.
	stepX, stepY := max(1, w/(gridSide*6)), max(1, h/(gridSide*6))
	sum := make([]uint32, gridSide*gridSide)
	count := make([]uint32, gridSide*gridSide)
	for y := 0; y < h; y += stepY {
		row := y * gridSide / h * gridSide
		for x := 0; x < w; x += stepX {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			cell := row + x*gridSide/w
			sum[cell] += (299*(r>>8) + 587*(g>>8) + 114*(bl>>8)) / 1000
			count[cell]++
		}
	}
	grid := make([]uint8, len(sum))
	for i := range sum {
		if count[i] > 0 {
			grid[i] = uint8(sum[i] / count[i])
		}
	}
	return grid
}

func (o outcome) same(p outcome) bool {
	if o.grid == nil || p.grid == nil {
		return o.grid == nil && p.grid == nil && o.failure != "" && o.failure == p.failure
	}
	for i := range o.grid {
		if d := int(o.grid[i]) - int(p.grid[i]); d > sameCell || d < -sameCell {
			return false
		}
	}
	return true
}

// sameArgs says whether two calls of a tool ask for the same thing.
func sameArgs(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		an, aIs := av.(float64)
		bn, bIs := bv.(float64)
		if aIs && bIs {
			if math.Abs(an-bn) > sameArg {
				return false
			}
			continue
		}
		ar, _ := json.Marshal(av)
		br, _ := json.Marshal(bv)
		if !bytes.Equal(ar, br) {
			return false
		}
	}
	return true
}

// note takes one action and what followed it. An action that repeats an
// earlier one and ends where that one ended goes one deeper into the
// circle. Anything new comes one step out.
func (b *brake) note(tool string, args map[string]any, after outcome) brakeSays {
	if passive[tool] {
		return brakeQuiet
	}
	var was *attempt
	for _, t := range b.tried {
		if t.tool == tool && sameArgs(t.args, args) {
			was = t
			break
		}
	}
	if was == nil {
		was = &attempt{tool: tool, args: args}
		b.tried = append(b.tried, was)
	}
	circling := false
	for _, seen := range was.seen {
		if seen.same(after) {
			circling = true
			break
		}
	}
	if !circling {
		was.seen = append(was.seen, after)
		b.depth = max(0, b.depth-1)
		return brakeQuiet
	}
	b.depth++
	switch {
	case b.depth >= brakeStop:
		return brakeStops
	case b.depth >= brakeWarn && !b.warned:
		b.warned = true
		return brakeWarns
	}
	return brakeQuiet
}
