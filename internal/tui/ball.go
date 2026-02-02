package tui

import (
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/harmonica"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	gravity       = 50.0 // pixels per second^2
	bounceDamping = 0.7  // energy retained on bounce
	friction      = 0.99 // velocity decay per frame
	ballSize      = 4    // ball is 4x4 characters
	grabRadius    = 6    // click within this distance to grab
	hoopWidth     = 10   // width of the hoop opening
	hoopDetectPad = 3    // extra padding on each side for score detection
)

// BallGame represents the basketball game.
type BallGame struct {
	x, y     float64 // ball position
	vx, vy   float64 // velocity
	width    int
	height   int

	// Mouse interaction
	grabbed  bool
	grabX    int
	grabY    int
	lastX    int
	lastY    int
	lastTime time.Time

	// Harmonica spring for smooth mouse following when grabbed
	springX harmonica.Spring
	springY harmonica.Spring

	// Visual
	ballStyle      lipgloss.Style
	trailPositions []struct{ x, y float64 }

	// Basketball hoop
	hoopX    int  // center x position of hoop
	hoopY    int  // y position of hoop rim
	score    int  // current score
	wasAbove bool // was ball above the hoop last frame (for scoring detection)
}

// NewBallGame creates a new basketball game.
func NewBallGame(width, height int) *BallGame {
	return &BallGame{
		x:       float64(width) / 2,
		y:       float64(height) * 2 / 3, // start in lower third
		vx:      0,
		vy:      0,
		width:   width,
		height:  height,
		springX: harmonica.NewSpring(harmonica.FPS(60), 8.0, 0.4),
		springY: harmonica.NewSpring(harmonica.FPS(60), 8.0, 0.4),
		ballStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("208")). // orange for basketball
			Bold(true),
		trailPositions: make([]struct{ x, y float64 }, 0, 10),
		hoopX:          width * 3 / 4,  // hoop on right side
		hoopY:          height / 4,     // top third
		score:          0,
		wasAbove:       false,
	}
}

// TickMsg is sent to update the ball physics.
type TickMsg time.Time

// Tick returns a command that sends tick messages for animation.
func Tick() tea.Cmd {
	return tea.Tick(time.Second/60, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// Update processes a frame of the ball physics.
func (b *BallGame) Update(width, height int) {
	b.width = width
	b.height = height

	// Update hoop position based on screen size
	b.hoopX = width * 3 / 4
	b.hoopY = height / 4

	// Track ball position relative to hoop for scoring
	ballCenterX := b.x + float64(ballSize)/2
	ballCenterY := b.y + float64(ballSize)/2
	// Use wider detection zone than visual rim
	hoopLeft := float64(b.hoopX - hoopWidth/2 - hoopDetectPad)
	hoopRight := float64(b.hoopX + hoopWidth/2 + hoopDetectPad)
	hoopYf := float64(b.hoopY)

	// Check if ball is currently above the hoop rim (with some tolerance)
	isAbove := ballCenterY < hoopYf+2

	if b.grabbed {
		// When grabbed, ball follows mouse with spring physics
		targetX := float64(b.grabX)
		targetY := float64(b.grabY)

		b.x, b.vx = b.springX.Update(b.x, b.vx, targetX)
		b.y, b.vy = b.springY.Update(b.y, b.vy, targetY)
	} else {
		// Apply gravity
		b.vy += gravity / 60.0

		// Apply friction
		b.vx *= friction
		b.vy *= friction

		// Update position
		b.x += b.vx / 60.0 * 10
		b.y += b.vy / 60.0 * 10

		// Bounce off walls (account for ball size)
		// Left wall
		if b.x < 0 {
			b.x = 0
			b.vx = -b.vx * bounceDamping
		}
		// Right wall (ball is ballSize wide)
		if b.x > float64(b.width-ballSize) {
			b.x = float64(b.width - ballSize)
			b.vx = -b.vx * bounceDamping
		}
		// Top wall
		if b.y < 0 {
			b.y = 0
			b.vy = -b.vy * bounceDamping
		}
		// Bottom wall (ball is ballSize tall)
		if b.y > float64(b.height-ballSize) {
			b.y = float64(b.height - ballSize)
			b.vy = -b.vy * bounceDamping
		}

		// Check for score: ball was above hoop, now below, moving mostly down, and within hoop width
		// Allow some upward velocity (vy > -5) to be more forgiving
		if b.wasAbove && !isAbove && b.vy > -5 {
			if ballCenterX >= hoopLeft && ballCenterX <= hoopRight {
				b.score++
			}
		}
	}

	b.wasAbove = isAbove

	// Update trail
	b.trailPositions = append(b.trailPositions, struct{ x, y float64 }{b.x, b.y})
	if len(b.trailPositions) > 8 {
		b.trailPositions = b.trailPositions[1:]
	}
}

// HandleMouse processes mouse input for grabbing/throwing the ball.
func (b *BallGame) HandleMouse(msg tea.MouseMsg) {
	switch msg.Button {
	case tea.MouseButtonLeft:
		if msg.Action == tea.MouseActionPress {
			// Check if clicking near the ball center (account for ball size)
			centerX := b.x + float64(ballSize)/2
			centerY := b.y + float64(ballSize)/2
			dx := float64(msg.X) - centerX
			dy := float64(msg.Y) - centerY
			dist := math.Sqrt(dx*dx + dy*dy)

			if dist < grabRadius {
				b.grabbed = true
				b.grabX = msg.X
				b.grabY = msg.Y
				b.lastX = msg.X
				b.lastY = msg.Y
				b.lastTime = time.Now()
			}
		} else if msg.Action == tea.MouseActionRelease && b.grabbed {
			// Release - calculate throw velocity from recent movement
			now := time.Now()
			dt := now.Sub(b.lastTime).Seconds()
			if dt > 0 && dt < 0.5 {
				b.vx = float64(msg.X-b.lastX) / dt * 0.5
				b.vy = float64(msg.Y-b.lastY) / dt * 0.5
			}
			b.grabbed = false
		}
	case tea.MouseButtonNone:
		// Mouse motion while grabbed
		if b.grabbed {
			// Track for velocity calculation
			b.lastX = b.grabX
			b.lastY = b.grabY
			b.lastTime = time.Now()

			b.grabX = msg.X
			b.grabY = msg.Y
		}
	}
}

// Render draws the ball, hoop, and score on the screen.
func (b *BallGame) Render(width, height int) string {
	var result strings.Builder

	// Create a 2D grid
	grid := make([][]rune, height)
	for i := range grid {
		grid[i] = make([]rune, width)
		for j := range grid[i] {
			grid[i][j] = ' '
		}
	}

	// Draw hoop (rim is a horizontal line, with backboard)
	// Rim: ╭──────╮
	// Net hangs below (optional decoration)
	hoopLeft := b.hoopX - hoopWidth/2
	hoopRight := b.hoopX + hoopWidth/2

	// Draw rim
	if b.hoopY >= 0 && b.hoopY < height {
		if hoopLeft >= 0 && hoopLeft < width {
			grid[b.hoopY][hoopLeft] = '('
		}
		for x := hoopLeft + 1; x < hoopRight; x++ {
			if x >= 0 && x < width {
				grid[b.hoopY][x] = '─'
			}
		}
		if hoopRight >= 0 && hoopRight < width {
			grid[b.hoopY][hoopRight] = ')'
		}
	}

	// Draw backboard (vertical line on the right side of hoop)
	backboardX := hoopRight + 1
	if backboardX >= 0 && backboardX < width {
		for y := b.hoopY - 2; y <= b.hoopY + 1; y++ {
			if y >= 0 && y < height {
				grid[y][backboardX] = '┃'
			}
		}
	}

	// Draw simple net below rim
	if b.hoopY+1 < height {
		for x := hoopLeft + 1; x < hoopRight; x++ {
			if x >= 0 && x < width {
				grid[b.hoopY+1][x] = '╎'
			}
		}
	}
	if b.hoopY+2 < height {
		for x := hoopLeft + 2; x < hoopRight-1; x++ {
			if x >= 0 && x < width {
				grid[b.hoopY+2][x] = '╎'
			}
		}
	}

	// Draw trail (fading)
	trailChars := []rune{'·', '·', '∘', '∘', '○', '○', '●', '●'}

	for i, pos := range b.trailPositions {
		tx, ty := int(pos.x), int(pos.y)
		if tx >= 0 && tx < width && ty >= 0 && ty < height {
			idx := i * len(trailChars) / len(b.trailPositions)
			if idx >= len(trailChars) {
				idx = len(trailChars) - 1
			}
			grid[ty][tx] = trailChars[idx]
		}
	}

	// Draw ball as 4x4 block
	bx, by := int(b.x), int(b.y)
	for dy := 0; dy < ballSize; dy++ {
		for dx := 0; dx < ballSize; dx++ {
			px, py := bx+dx, by+dy
			if px >= 0 && px < width && py >= 0 && py < height {
				grid[py][px] = '█'
			}
		}
	}

	// Styles
	ballStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true) // orange
	trailStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	hoopStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // red
	netStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("255"))  // white

	// Convert grid to string
	for y := 0; y < height; y++ {
		line := ""
		for x := 0; x < width; x++ {
			ch := grid[y][x]
			switch ch {
			case '█':
				line += ballStyle.Render(string(ch))
			case '(', ')', '─', '┃':
				line += hoopStyle.Render(string(ch))
			case '╎':
				line += netStyle.Render(string(ch))
			case '·', '∘', '○', '●':
				line += trailStyle.Render(string(ch))
			default:
				line += " "
			}
		}
		result.WriteString(line)
		result.WriteString("\n")
	}

	return result.String()
}

// Score returns the current score.
func (b *BallGame) Score() int {
	return b.score
}


// IsGrabbed returns whether the ball is currently grabbed.
func (b *BallGame) IsGrabbed() bool {
	return b.grabbed
}
