/**
 * Tokenisation: lower-cased, whitespace-split. Matches
 * cli/parser/source.go's `NewSource(strings.ToLower(input))` shape.
 */
export function tokenise(input: string): string[] {
  const trimmed = input.trim().toLowerCase();
  if (trimmed === "") {
    return [];
  }
  return trimmed.split(/\s+/);
}
