import { formatSendSuggestion, htmlToPlainText, plainTextToHtml, REACTION_EMOJIS } from './mail-extras';

describe('htmlToPlainText', () => {
  it('returns an empty string for null/undefined/empty input', () => {
    expect(htmlToPlainText(null)).toBe('');
    expect(htmlToPlainText(undefined)).toBe('');
    expect(htmlToPlainText('')).toBe('');
  });

  it('converts <br> and block-closing tags into newlines', () => {
    expect(htmlToPlainText('<p>Best,<br/>Jordan</p>')).toBe('Best,\nJordan');
    expect(htmlToPlainText('<div>Line one</div><div>Line two</div>')).toBe('Line one\nLine two');
  });

  it('strips remaining tags and decodes common entities', () => {
    expect(htmlToPlainText('<strong>Cole &amp; Co</strong>')).toBe('Cole & Co');
    expect(htmlToPlainText('<p>Tom &amp; Jerry&#39;s &quot;show&quot;</p>')).toBe(
      `Tom & Jerry's "show"`
    );
    expect(htmlToPlainText('<p>a&nbsp;b</p>')).toBe('a b');
  });

  it('collapses runs of 3+ newlines down to a single blank line and trims', () => {
    expect(htmlToPlainText('<p>a</p><p></p><p></p><p>b</p>')).toBe('a\n\nb');
  });
});

describe('plainTextToHtml', () => {
  it('escapes HTML-significant characters and converts newlines to <br/>', () => {
    expect(plainTextToHtml('Best,\nJordan')).toBe('Best,<br/>Jordan');
    expect(plainTextToHtml('Cole & Co <ok>')).toBe('Cole &amp; Co &lt;ok&gt;');
  });

  it('round-trips through htmlToPlainText for simple multi-line text', () => {
    const original = 'Best,\nJordan\nCalendium';
    expect(htmlToPlainText(plainTextToHtml(original))).toBe(original);
  });
});

describe('REACTION_EMOJIS', () => {
  it('has exactly the five Superhuman-style quick reactions', () => {
    expect(REACTION_EMOJIS).toEqual(['👍', '❤️', '😂', '🎉', '✅']);
  });
});

describe('formatSendSuggestion', () => {
  it('mentions "Smart Send" and the sample size', () => {
    const text = formatSendSuggestion({
      email: 'sarah@acme.com',
      suggestedAt: new Date('2026-07-20T14:00:00Z').toISOString(),
      utcOffsetHours: -5,
      confidence: 0.8,
      sampleSize: 12,
    });
    expect(text).toContain('Smart Send');
    expect(text).toContain('12 past opens');
  });

  it('singularizes a sample size of 1', () => {
    const text = formatSendSuggestion({
      email: 'sarah@acme.com',
      suggestedAt: new Date().toISOString(),
      utcOffsetHours: 0,
      confidence: 0.5,
      sampleSize: 1,
    });
    expect(text).toContain('1 past open)');
  });
});
