import { fireEvent, render, screen } from '@testing-library/react-native';
import { ApiRequestError } from '@calendium/shared';
import { ThreadAiPanel } from './thread-ai-panel';

describe('ThreadAiPanel', () => {
  it('renders nothing when AI is disabled for this server, even with data to show', async () => {
    await render(
      <ThreadAiPanel
        enabled={false}
        summary="The sender needs a decision by Thursday."
        repliesLoading={false}
        replies={['Sounds good!']}
        onSelectReply={jest.fn()}
      />
    );
    expect(screen.queryByText('The sender needs a decision by Thursday.')).toBeNull();
    expect(screen.queryByText('Sounds good!')).toBeNull();
  });

  it('renders the summary line from thread.summary', async () => {
    await render(
      <ThreadAiPanel
        enabled
        summary="The sender needs a decision by Thursday."
        repliesLoading={false}
        replies={[]}
        onSelectReply={jest.fn()}
      />
    );
    expect(screen.getByText('The sender needs a decision by Thursday.')).toBeTruthy();
  });

  it('renders nothing at all when there is no summary and no reply data (never fabricates content)', async () => {
    const { toJSON } = await render(
      <ThreadAiPanel
        enabled
        summary={undefined}
        repliesLoading={false}
        replies={[]}
        onSelectReply={jest.fn()}
      />
    );
    expect(toJSON()).toBeNull();
  });

  it('shows a loading state while instant replies are being fetched', async () => {
    await render(
      <ThreadAiPanel enabled summary={undefined} repliesLoading replies={[]} onSelectReply={jest.fn()} />
    );
    expect(screen.getByText('Loading suggested replies…')).toBeTruthy();
  });

  it('shows the daily AI limit message on a 429', async () => {
    await render(
      <ThreadAiPanel
        enabled
        summary={undefined}
        repliesLoading={false}
        repliesError={new ApiRequestError(429, 'rate_limited', 'too many requests')}
        replies={[]}
        onSelectReply={jest.fn()}
      />
    );
    expect(screen.getByText(/today's AI limit/)).toBeTruthy();
  });

  it('shows a generic error otherwise', async () => {
    await render(
      <ThreadAiPanel
        enabled
        summary={undefined}
        repliesLoading={false}
        repliesError={new Error('network down')}
        replies={[]}
        onSelectReply={jest.fn()}
      />
    );
    expect(screen.getByText("Couldn't load suggested replies.")).toBeTruthy();
  });

  it('prefills the reply box when a chip is tapped', async () => {
    const onSelectReply = jest.fn();
    await render(
      <ThreadAiPanel
        enabled
        summary={undefined}
        repliesLoading={false}
        replies={['Sounds good, thanks!', "I'll take a look and follow up."]}
        onSelectReply={onSelectReply}
      />
    );

    fireEvent.press(screen.getByText('Sounds good, thanks!'));

    expect(onSelectReply).toHaveBeenCalledWith('Sounds good, thanks!');
    expect(onSelectReply).toHaveBeenCalledTimes(1);
  });
});
