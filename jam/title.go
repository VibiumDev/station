package jam

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

// TitleLimit is how long a jam's title may be, in characters.
const TitleLimit = 60

// ownTitle is how short a prompt is its own title.
const ownTitle = 48

const titleAsk = "Give the task below a short title for a list: two to six words that say what the task is. " +
	"Answer with the title alone, in the task's language. No quotes, and no period at the end."

// Title names a prompt in a few words, for lists. A prompt that is already
// that short is its own title and costs nothing. Otherwise the model is
// asked, in one small request with no tools.
func (m *Model) Title(ctx context.Context, prompt string) (string, Usage, error) {
	line := strings.Join(strings.Fields(prompt), " ")
	if line == "" {
		return "", Usage{}, errors.New("no prompt to name")
	}
	if utf8.RuneCountInString(line) <= ownTitle {
		return strings.TrimRight(line, "."), Usage{}, nil
	}
	effort := m.ReasoningEffort
	if effort == "" && m.Provider == "openai" {
		effort = OpenAIEffortDefault(m.Name)
	}
	reply, err := m.complete(ctx, []Message{{Role: "system", Content: titleAsk}, {Role: "user", Content: prompt}}, nil, "", effort)
	if err != nil {
		return "", Usage{}, err
	}
	text, _ := reply.Message.Content.(string)
	title := tidyTitle(text)
	if title == "" {
		return "", reply.Usage, errors.New("the model gave no title")
	}
	return title, reply.Usage, nil
}

// tidyTitle makes a model's answer a title: its first line, without the
// quotes, emphasis marks or final period a model adds anyway, and no
// longer than TitleLimit, cut at a word.
func tidyTitle(text string) string {
	for _, l := range strings.Split(text, "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			text = l
			break
		}
	}
	text = strings.TrimPrefix(strings.TrimSpace(text), "Title:")
	text = strings.Trim(strings.TrimSpace(text), "\"'*_“”‘’«»")
	text = strings.TrimRight(strings.TrimSpace(text), ".")
	r := []rune(text)
	if len(r) <= TitleLimit {
		return text
	}
	cut := TitleLimit - 1
	for i := cut; i > cut-20 && i > 0; i-- {
		if r[i] == ' ' {
			cut = i
			break
		}
	}
	return strings.TrimRight(string(r[:cut]), " ,.;:") + "…"
}
