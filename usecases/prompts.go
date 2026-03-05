package usecases

import "fmt"

// CommitMessageGenerationPrompt creates a prompt for Claude to generate commit messages
func CommitMessageGenerationPrompt(branchName string) string {
	return `Generate a commit message for the changes we just made in our conversation.

CRITICAL INSTRUCTIONS:
1. You MUST respond with ONLY the commit message text
2. NO explanations, NO additional text, NO formatting markup
3. NO "Here is the commit message:" or similar phrases
4. Maximum 50 characters total (STRICT LIMIT)
5. Start with action verb (Add, Fix, Update, etc.)
6. Use imperative mood
7. Base it on what we actually worked on in this conversation

FORMAT EXAMPLE:
Fix user authentication validation

YOUR RESPONSE MUST BE THE COMMIT MESSAGE ONLY.`
}

// PRTitleGenerationPrompt creates a prompt for Claude to generate PR titles.
// The prompt uses explicit good/bad examples and strong output constraints to prevent
// the model from generating a full PR description instead of just the title.
func PRTitleGenerationPrompt(branchName string) string {
	return `You are a PR title generator. Output ONLY a single-line PR title for the changes in this conversation.

Format: type: short description
Valid types: feat, fix, docs, chore, refactor, test, perf, ci, build, style

GOOD output examples:
  feat: add email validation for user signup
  fix: prevent nil pointer in user lookup
  chore: update Go dependencies to latest
  refactor: extract auth middleware into package

BAD output (NEVER do this):
  feat: add validation ## Summary - Added validation... (includes markdown/body text)
  "feat: add validation" (has quotes)
  Here is the PR title: feat: add validation (has preamble)
  feat: add validation. This change also updates... (has explanation)

RULES:
1. Output EXACTLY one line - no line breaks, no additional lines
2. Maximum 72 characters total
3. Use lowercase after the colon
4. No period at the end
5. No markdown (##, -, *, etc.)
6. No quotes around the title
7. No preamble or explanation - just the title itself
8. Do not mention "Claude" or "agent"

Your entire response must be the PR title and nothing else.`
}

// PRDescriptionGenerationPrompt creates a prompt for Claude to generate PR descriptions
func PRDescriptionGenerationPrompt(branchName string, prTemplate string) string {
	if prTemplate != "" {
		// Template exists - use it as a guideline
		return fmt.Sprintf(`Generate a pull request description for the work we completed in this conversation.

The repository has a PR template that you should follow as a guideline:

--- PR TEMPLATE ---
%s
--- END TEMPLATE ---

INSTRUCTIONS:
- Follow the structure and sections defined in the template above
- Fill in all relevant sections based on our conversation
- Keep it professional and concise
- If the template has checkboxes (- [ ]), include them in your response
- If the template has placeholders or instructions (like "describe your changes"), replace them with actual content
- Base your description on what we actually worked on in our conversation
- Use proper markdown formatting

IMPORTANT:
- Do NOT include any "Generated with nairid" or similar footer text. I will add that separately.
- Do NOT include any introductory text like "Here is your description"

CRITICAL: Your response must contain ONLY the PR description in markdown format. Do not include:
- Any explanations or reasoning about your response
- "Here is the description:" or similar phrases
- Any text before or after the description
- Any commentary about the changes
- Any other text whatsoever
- Do NOT execute any git or gh commands
- Do NOT create, update, or modify any pull requests
- Do NOT perform any actions - this is a text-only request

Respond with ONLY the PR description in markdown format, nothing else.`, prTemplate)
	}

	// No template - use default format
	return `Generate a concise pull request description for the work we completed in this conversation.

Format:
- ## Summary: High-level overview of what changed (2-3 bullet points max)
- ## Why: Brief explanation of the motivation/reasoning behind the change

Keep it professional but brief. Focus on WHAT changed at a high level and WHY the change was necessary, not detailed implementation specifics.

Use proper markdown formatting. Base it on what we actually worked on in our conversation.

IMPORTANT:
- Do NOT include any "Generated with nairid" or similar footer text. I will add that separately.
- Keep the summary concise - avoid listing every single file or detailed code changes
- Focus on the business/functional purpose of the changes
- Do NOT include any introductory text like "Here is your description"

CRITICAL: Your response must contain ONLY the PR description in markdown format. Do not include:
- Any explanations or reasoning about your response
- "Here is the description:" or similar phrases
- Any text before or after the description
- Any commentary about the changes
- Any other text whatsoever
- Do NOT execute any git or gh commands
- Do NOT create, update, or modify any pull requests
- Do NOT perform any actions - this is a text-only request

Respond with ONLY the PR description in markdown format, nothing else.`
}

// PRTitleUpdatePrompt creates a prompt for Claude to update existing PR titles.
// Uses explicit examples and strong output constraints to prevent the model from
// generating a full PR description instead of just the title.
func PRTitleUpdatePrompt(currentTitle, branchName string) string {
	return fmt.Sprintf(`You are a PR title generator. Review and optionally update this PR title based on our conversation.

Current title: %s

Only update if the current title is inaccurate or obsolete. Keep it unchanged if it still captures the main purpose.

Format: type: short description
Valid types: feat, fix, docs, chore, refactor, test, perf, ci, build, style

GOOD output examples:
  feat: add email validation for user signup
  fix: prevent nil pointer in user lookup

BAD output (NEVER do this):
  feat: add validation ## Summary - Added validation... (includes body text)
  "feat: add validation" (has quotes)
  Here is the updated title: feat: add validation (has preamble)

RULES:
1. Output EXACTLY one line - no line breaks, no additional lines
2. Maximum 72 characters total
3. Use lowercase after the colon
4. No period at the end
5. No markdown (##, -, *, etc.)
6. No quotes around the title
7. No preamble or explanation - just the title itself

Your entire response must be the PR title and nothing else.`, currentTitle)
}

// PRDescriptionUpdatePrompt creates a prompt for Claude to update existing PR descriptions
func PRDescriptionUpdatePrompt(currentDescriptionClean, branchName string) string {
	return fmt.Sprintf(`I have an existing pull request with this description:

CURRENT DESCRIPTION:
%s

Based on our ongoing conversation, review whether this description still accurately captures all the work we've done.

INSTRUCTIONS:
- Review the current description and what we've worked on in our conversation
- ONLY update the description if significant new functionality has been added that warrants description updates
- If the current description still accurately captures the work, return it unchanged (without footer)
- If updating, make it additive - enhance the existing description rather than replacing it
- Keep the same structure: ## Summary and ## Why sections
- Focus on WHAT changed at a high level and WHY the change was necessary
- Use proper markdown formatting
- Keep it professional but brief
- Do NOT mention implementation details

Examples of when to update:
- Current description only mentions "Fix auth bug" → New work adds complete user management → Update to include both
- Current description is "Add dashboard" → New work adds charts and filters → Update to "Add dashboard with charts and filtering"

Examples of when NOT to update:
- Current description covers "User authentication system" → New work just fixes small auth bugs → Keep current
- Current description mentions "Performance improvements" → New work makes minor tweaks → Keep current

IMPORTANT: 
- Do NOT include any "Generated with nairid" or similar footer text. I will add that separately.
- Return only the description content in markdown format, nothing else.
- If no update is needed, return the current description exactly as provided (minus any footer).

CRITICAL: Your response must contain ONLY the PR description in markdown format. Do not include:
- Any explanations or reasoning about your decision
- "Here is the updated description:" or similar phrases
- Commentary about whether you updated it or not
- Any text before or after the description
- Any analysis of the changes
- Any other text whatsoever
- Do NOT execute any git or gh commands
- Do NOT create, update, or modify any pull requests
- Do NOT perform any actions - this is a text-only request

Respond with ONLY the PR description in markdown format, nothing else.`, currentDescriptionClean)
}
