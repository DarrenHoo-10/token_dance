import { useEffect, useRef, useState } from 'react';
import type { TokenSceneController } from './tokenSceneRenderer';

export function TokenScene({ paused = false }: { paused?: boolean }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const controllerRef = useRef<TokenSceneController | null>(null);
  const pausedRef = useRef(paused);
  const [rendererReady, setRendererReady] = useState(false);

  useEffect(() => {
    pausedRef.current = paused;
    controllerRef.current?.setPaused(paused);
  }, [paused]);

  useEffect(() => {
    if (!canvasRef.current || typeof window.matchMedia !== 'function') return;
    const canvas = canvasRef.current;
    const desktop = window.matchMedia('(min-width: 960px)');
    const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
    let disposed = false;
    let request = 0;

    const syncRenderer = async () => {
      if (!desktop.matches || reducedMotion.matches) {
        request++;
        controllerRef.current?.dispose();
        controllerRef.current = null;
        setRendererReady(false);
        return;
      }
      if (controllerRef.current) return;
      const currentRequest = ++request;
      try {
        const { createTokenScene } = await import('./tokenSceneRenderer');
        if (disposed || currentRequest !== request) return;
        const controller = createTokenScene(canvas);
        controllerRef.current = controller;
        controller.setPaused(pausedRef.current);
        await controller.ready;
        if (disposed || currentRequest !== request || controllerRef.current !== controller) return;
        setRendererReady(true);
      } catch {
        // Keep the original artwork usable on devices without WebGL.
        if (!disposed && currentRequest === request) {
          controllerRef.current?.dispose();
          controllerRef.current = null;
          setRendererReady(false);
        }
      }
    };
    const handleContextLost = (event: Event) => {
      event.preventDefault();
      request++;
      controllerRef.current?.dispose();
      controllerRef.current = null;
      setRendererReady(false);
    };
    const handleContextRestored = () => { void syncRenderer(); };
    desktop.addEventListener('change', syncRenderer);
    reducedMotion.addEventListener('change', syncRenderer);
    canvas.addEventListener('webglcontextlost', handleContextLost);
    canvas.addEventListener('webglcontextrestored', handleContextRestored);
    void syncRenderer();
    return () => {
      disposed = true;
      request++;
      desktop.removeEventListener('change', syncRenderer);
      reducedMotion.removeEventListener('change', syncRenderer);
      canvas.removeEventListener('webglcontextlost', handleContextLost);
      canvas.removeEventListener('webglcontextrestored', handleContextRestored);
      controllerRef.current?.dispose();
      controllerRef.current = null;
    };
  }, []);

  return (
    <div className="token-scene" data-renderer={rendererReady ? 'webgl' : 'image'} data-paused={paused} aria-hidden="true">
      <picture className="token-scene__background">
        <source media="(max-width: 959px)" srcSet="data:image/gif;base64,R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs=" />
        <img src={`${import.meta.env.BASE_URL}images/auth-landscape.webp`} alt="" width="1254" height="1254" {...{ fetchpriority: 'high' }} />
      </picture>
      <div className="token-scene__ribbon">
        <picture>
          <source media="(max-width: 959px)" srcSet="data:image/gif;base64,R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs=" />
          <img src={`${import.meta.env.BASE_URL}images/auth-ribbon-foreground.webp`} alt="" width="1254" height="1254" {...{ fetchpriority: 'high' }} />
        </picture>
      </div>
      <canvas ref={canvasRef} className="token-scene__canvas" />
    </div>
  );
}

export type CompanionMood = 'idle' | 'email' | 'password' | 'loading' | 'error';

export function LoginCompanions({ mood, caption }: { mood: CompanionMood; caption: string }) {
  return (
    <div className="login-companions" data-mood={mood} aria-hidden="true">
      <span className="login-companions__caption" key={mood}>{caption}</span>
      <div className="login-companions__friends">
        {['lime', 'cream'].map((color) => (
          <div className={`login-friend login-friend--${color}`} key={color}>
            <span className="login-friend__tuft" />
            <div className="login-friend__face">
              <span className="login-friend__eye" /><span className="login-friend__eye" />
              <span className="login-friend__cheek login-friend__cheek--left" />
              <span className="login-friend__cheek login-friend__cheek--right" />
              <span className="login-friend__mouth" />
            </div>
            <span className="login-friend__hand login-friend__hand--left" />
            <span className="login-friend__hand login-friend__hand--right" />
          </div>
        ))}
      </div>
      <span className="login-companions__ground" />
    </div>
  );
}
