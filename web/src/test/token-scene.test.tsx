import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, waitFor } from '@testing-library/react';
import { TokenScene } from '@/pages/auth/LoginArt';

const { createTokenScene } = vi.hoisted(() => ({ createTokenScene: vi.fn() }));
vi.mock('@/pages/auth/tokenSceneRenderer', () => ({ createTokenScene }));

class MediaQuery extends EventTarget {
  constructor(public matches: boolean) { super(); }
  setMatches(matches: boolean) {
    this.matches = matches;
    this.dispatchEvent(new Event('change'));
  }
}

describe('Login scene lifecycle', () => {
  let desktop: MediaQuery;
  let reducedMotion: MediaQuery;
  let controllers: Array<{ setPaused: ReturnType<typeof vi.fn>; dispose: ReturnType<typeof vi.fn> }>;

  beforeEach(() => {
    desktop = new MediaQuery(true);
    reducedMotion = new MediaQuery(false);
    controllers = [];
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => (
      query.includes('min-width') ? desktop : reducedMotion
    ) as unknown as MediaQueryList);
    createTokenScene.mockReset().mockImplementation(() => {
      const controller = { setPaused: vi.fn(), dispose: vi.fn() };
      controllers.push(controller);
      return controller;
    });
  });

  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it('honors pause before lazy loading completes, resumes and releases resources on exit', async () => {
    const view = render(<TokenScene paused />);
    await waitFor(() => expect(view.container.firstChild).toHaveAttribute('data-renderer', 'webgl'));
    expect(controllers[0].setPaused).toHaveBeenLastCalledWith(true);
    view.rerender(<TokenScene paused={false} />);
    expect(controllers[0].setPaused).toHaveBeenLastCalledWith(false);
    view.unmount();
    expect(controllers[0].dispose).toHaveBeenCalledTimes(1);
  });

  it('only creates a renderer on desktop with motion enabled and disposes it when either changes', async () => {
    desktop.matches = false;
    const view = render(<TokenScene />);
    await act(async () => {});
    expect(createTokenScene).not.toHaveBeenCalled();
    await act(async () => { desktop.setMatches(true); });
    await waitFor(() => expect(createTokenScene).toHaveBeenCalledTimes(1));
    await act(async () => { reducedMotion.setMatches(true); });
    expect(controllers[0].dispose).toHaveBeenCalledTimes(1);
    expect(view.container.firstChild).toHaveAttribute('data-renderer', 'image');
    await act(async () => { reducedMotion.setMatches(false); });
    await waitFor(() => expect(createTokenScene).toHaveBeenCalledTimes(2));
    await act(async () => { desktop.setMatches(false); });
    expect(controllers[1].dispose).toHaveBeenCalledTimes(1);
    expect(view.container.firstChild).toHaveAttribute('data-renderer', 'image');
  });

  it('keeps the static artwork when WebGL cannot initialize', async () => {
    createTokenScene.mockImplementation(() => { throw new Error('WebGL unavailable'); });
    const view = render(<TokenScene />);
    await waitFor(() => expect(createTokenScene).toHaveBeenCalledTimes(1));
    expect(view.container.firstChild).toHaveAttribute('data-renderer', 'image');
  });

  it('keeps the original artwork visible until both scene images are ready', async () => {
    let finishLoading!: () => void;
    const controller = {
      ready: new Promise<void>((resolve) => { finishLoading = resolve; }),
      setPaused: vi.fn(), dispose: vi.fn(),
    };
    createTokenScene.mockReturnValueOnce(controller);
    const view = render(<TokenScene />);
    await waitFor(() => expect(createTokenScene).toHaveBeenCalledTimes(1));
    expect(view.container.firstChild).toHaveAttribute('data-renderer', 'image');
    view.rerender(<TokenScene paused />);
    expect(controller.setPaused).toHaveBeenLastCalledWith(true);
    await act(async () => { finishLoading(); });
    expect(view.container.firstChild).toHaveAttribute('data-renderer', 'webgl');
  });

  it('releases the renderer and preserves the fallback after an image fails to load', async () => {
    let failLoading!: (error: Error) => void;
    const controller = {
      ready: new Promise<void>((_, reject) => { failLoading = reject; }),
      setPaused: vi.fn(), dispose: vi.fn(),
    };
    createTokenScene.mockReturnValueOnce(controller);
    const view = render(<TokenScene />);
    await waitFor(() => expect(createTokenScene).toHaveBeenCalledTimes(1));
    await act(async () => { failLoading(new Error('Image unavailable')); });
    expect(controller.dispose).toHaveBeenCalledTimes(1);
    expect(view.container.firstChild).toHaveAttribute('data-renderer', 'image');
  });

  it('ignores a pending image load after switching to mobile', async () => {
    let finishLoading!: () => void;
    const controller = {
      ready: new Promise<void>((resolve) => { finishLoading = resolve; }),
      setPaused: vi.fn(), dispose: vi.fn(),
    };
    createTokenScene.mockReturnValueOnce(controller);
    const view = render(<TokenScene />);
    await waitFor(() => expect(createTokenScene).toHaveBeenCalledTimes(1));
    await act(async () => { desktop.setMatches(false); });
    expect(controller.dispose).toHaveBeenCalledTimes(1);
    await act(async () => { finishLoading(); });
    expect(view.container.firstChild).toHaveAttribute('data-renderer', 'image');
  });

  it('falls back on context loss and rebuilds after context restoration', async () => {
    const view = render(<TokenScene />);
    await waitFor(() => expect(view.container.firstChild).toHaveAttribute('data-renderer', 'webgl'));
    const canvas = view.container.querySelector('canvas')!;
    fireEvent(canvas, new Event('webglcontextlost', { cancelable: true }));
    expect(controllers[0].dispose).toHaveBeenCalledTimes(1);
    expect(view.container.firstChild).toHaveAttribute('data-renderer', 'image');
    fireEvent(canvas, new Event('webglcontextrestored'));
    await waitFor(() => expect(view.container.firstChild).toHaveAttribute('data-renderer', 'webgl'));
    expect(createTokenScene).toHaveBeenCalledTimes(2);
  });

  it('does not allocate WebGL after unmounting during lazy loading', async () => {
    const view = render(<TokenScene />);
    view.unmount();
    await act(async () => {});
    expect(createTokenScene).not.toHaveBeenCalled();
  });
});
