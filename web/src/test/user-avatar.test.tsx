import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { UserAvatar } from '@/components/common/UserAvatar';

it('replaces a failed image with an initial and retries a replacement URL', () => {
  const { rerender, container } = render(<UserAvatar url="/api/v1/public/avatars/old" name="Jiayu" fallbackClassName="list-avatar" alt="avatar" />);
  fireEvent.error(screen.getByRole('img', { name: 'avatar' }));
  expect(screen.queryByRole('img')).not.toBeInTheDocument();
  expect(screen.getByText('J')).toHaveClass('list-avatar');
  rerender(<UserAvatar url="/api/v1/public/avatars/new" name="Jiayu" alt="avatar" />);
  expect(screen.getByRole('img')).toHaveAttribute('src', '/api/v1/public/avatars/new');
  expect(container.textContent).toBe('');
});
