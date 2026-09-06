import React from 'react';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
vi.mock('../src/orb/bridge',()=>({orbAction:vi.fn().mockResolvedValue(undefined),orbBeginDrag:vi.fn().mockResolvedValue(undefined),orbEndDrag:vi.fn().mockResolvedValue(undefined),orbMove:vi.fn().mockResolvedValue(undefined),orbFling:vi.fn().mockResolvedValue(undefined)}));
import { orbAction, orbBeginDrag, orbEndDrag, orbFling, orbMove } from '../src/orb/bridge';
import { useOrbGesture } from '../src/orb/useOrbGesture';
function TestOrb(){const g=useOrbGesture(false);return <button data-power={g.pull.power} {...{onPointerDown:g.onPointerDown,onPointerMove:g.onPointerMove,onPointerUp:g.onPointerUp,onDoubleClick:g.onDoubleClick,onPointerCancel:g.onPointerCancel,onLostPointerCapture:g.onLostPointerCapture}} onContextMenu={e=>e.preventDefault()}>{g.charging?'charging':'idle'}</button>;}
class TestPointerEvent extends MouseEvent {pointerId:number; constructor(type:string,options:PointerEventInit={}){super(type,options);this.pointerId=options.pointerId??1;}}
const flush=()=>act(async()=>{await new Promise(resolve=>setTimeout(resolve,0));});
beforeEach(()=>{vi.clearAllMocks();window.PointerEvent=TestPointerEvent as unknown as typeof PointerEvent;HTMLElement.prototype.setPointerCapture=vi.fn();HTMLElement.prototype.hasPointerCapture=()=>true;HTMLElement.prototype.releasePointerCapture=vi.fn();});
afterEach(cleanup);
describe('orb pointer gestures',()=>{
 it('cancelling a press releases the paused edge slide without activating',async()=>{
  render(<TestOrb/>);const ball=screen.getByRole('button');
  fireEvent.pointerDown(ball,{button:0,pointerId:1});fireEvent.pointerCancel(ball,{pointerId:1});await flush();
  expect(vi.mocked(orbAction).mock.calls).toEqual([['grab'],['release_grab']]);
  expect(orbBeginDrag).not.toHaveBeenCalled();expect(orbEndDrag).not.toHaveBeenCalled();
 });
 it('rapid consecutive drags retain their own final coordinates',async()=>{
  render(<TestOrb/>);const ball=screen.getByRole('button');
  for(const [id,dx] of [[1,100],[2,25]]){
   fireEvent.pointerDown(ball,{button:0,pointerId:id,screenX:100,screenY:100});
   fireEvent.pointerMove(ball,{pointerId:id,screenX:100+dx,screenY:100});
   fireEvent.pointerUp(ball,{button:0,pointerId:id,screenX:100+dx,screenY:100});
  }
  await flush();expect(vi.mocked(orbMove).mock.calls).toEqual([[100,0],[25,0]]);expect(orbEndDrag).toHaveBeenCalledTimes(2);
 });
 it('right pull moves the ball before launching once, without docking or opening main',async()=>{
  render(<TestOrb/>);const ball=screen.getByRole('button');
  fireEvent.pointerDown(ball,{button:2,pointerId:1,screenX:100,screenY:100});await flush();expect(ball.textContent).toBe('charging');
  expect(fireEvent.contextMenu(ball)).toBe(false);
  fireEvent.pointerMove(ball,{pointerId:1,screenX:130,screenY:80});await flush();
  expect(orbMove).toHaveBeenLastCalledWith(30,-20);expect(Number(ball.dataset.power)).toBeGreaterThan(0);
  fireEvent.pointerUp(ball,{button:2,pointerId:1,screenX:150,screenY:80});await flush();
  expect(orbMove).toHaveBeenLastCalledWith(50,-20);
  expect(orbFling).toHaveBeenCalledExactlyOnceWith(50,-20);
  expect(vi.mocked(orbMove).mock.invocationCallOrder.at(-1)).toBeLessThan(vi.mocked(orbFling).mock.invocationCallOrder[0]);
  expect(orbAction).toHaveBeenCalledExactlyOnceWith('begin_pull');expect(orbBeginDrag).not.toHaveBeenCalled();expect(orbEndDrag).not.toHaveBeenCalled();
 });
 it('stationary hold and pulling back to the anchor never launch',async()=>{
  render(<TestOrb/>);const ball=screen.getByRole('button');
  fireEvent.pointerDown(ball,{button:2,pointerId:1,screenX:200,screenY:100});await flush();
  expect(ball.dataset.power).toBe('0');
  fireEvent.pointerUp(ball,{button:2,pointerId:1,screenX:200,screenY:100});await flush();
  expect(orbFling).not.toHaveBeenCalled();expect(orbAction).toHaveBeenLastCalledWith('cancel_pull');
  fireEvent.pointerDown(ball,{button:2,pointerId:2,screenX:200,screenY:100});
  fireEvent.pointerMove(ball,{pointerId:2,screenX:120,screenY:100});expect(ball.dataset.power).toBe('0.5');
  fireEvent.pointerMove(ball,{pointerId:2,screenX:0,screenY:100});expect(ball.dataset.power).toBe('1.25');
  fireEvent.pointerMove(ball,{pointerId:2,screenX:200,screenY:100});expect(ball.dataset.power).toBe('0');
  fireEvent.pointerUp(ball,{button:2,pointerId:2,screenX:200,screenY:100});await flush();expect(orbFling).not.toHaveBeenCalled();
 });
 it('pulling beyond 160 keeps increasing power and sends the full release distance',async()=>{
  render(<TestOrb/>);const ball=screen.getByRole('button');
  fireEvent.pointerDown(ball,{button:2,pointerId:1,screenX:1000,screenY:100});
  fireEvent.pointerMove(ball,{pointerId:1,screenX:680,screenY:100});expect(ball.dataset.power).toBe('2');
  fireEvent.pointerMove(ball,{pointerId:1,screenX:360,screenY:100});expect(ball.dataset.power).toBe('4');
  fireEvent.pointerUp(ball,{button:2,pointerId:1,screenX:360,screenY:100});await flush();
  expect(orbMove).toHaveBeenLastCalledWith(-640,0);expect(orbFling).toHaveBeenCalledExactlyOnceWith(-640,0);
  expect(orbEndDrag).not.toHaveBeenCalled();
 });
 it('cancel or focus loss discards a charged shot',async()=>{
  render(<TestOrb/>);const ball=screen.getByRole('button');
  fireEvent.pointerDown(ball,{button:2,pointerId:1});fireEvent.pointerCancel(ball,{pointerId:1});fireEvent.pointerUp(ball,{button:2,pointerId:1});await flush();expect(orbFling).not.toHaveBeenCalled();
  fireEvent.pointerDown(ball,{button:2,pointerId:2});fireEvent.pointerMove(ball,{pointerId:2,screenX:80,screenY:0});fireEvent(window,new Event('blur'));fireEvent.pointerUp(ball,{button:2,pointerId:2});await flush();expect(orbFling).not.toHaveBeenCalled();expect(ball.textContent).toBe('idle');
  expect(orbAction).toHaveBeenLastCalledWith('cancel_pull');expect(orbEndDrag).not.toHaveBeenCalled();
 });
 it('single click never opens main; dragging preserves final position',async()=>{
  render(<TestOrb/>);const ball=screen.getByRole('button');
  fireEvent.pointerDown(ball,{button:0,pointerId:1,screenX:100,screenY:100});fireEvent.pointerUp(ball,{button:0,pointerId:1,screenX:100,screenY:100});await flush();expect(orbAction).toHaveBeenLastCalledWith('reveal');expect(orbAction).not.toHaveBeenCalledWith('activate');vi.clearAllMocks();
  fireEvent.pointerDown(ball,{button:0,pointerId:2,screenX:100,screenY:100});fireEvent.pointerMove(ball,{pointerId:2,screenX:120,screenY:110});fireEvent.pointerMove(ball,{pointerId:2,screenX:180,screenY:140});fireEvent.pointerUp(ball,{button:0,pointerId:2,screenX:200,screenY:150});await flush();
  expect(orbBeginDrag).toHaveBeenCalledTimes(1);expect(orbMove).toHaveBeenLastCalledWith(100,50);expect(orbEndDrag).toHaveBeenCalledTimes(1);expect(orbAction).toHaveBeenCalledExactlyOnceWith('grab');
 });
 it('only a double-click made of two actual clicks opens main once',async()=>{
  render(<TestOrb/>);const ball=screen.getByRole('button');
  for(let id=1;id<=2;id++) {
   fireEvent.pointerDown(ball,{button:0,pointerId:id,screenX:100,screenY:100});
   fireEvent.pointerUp(ball,{button:0,pointerId:id,screenX:100,screenY:100});
  }
  await flush();expect(orbAction).not.toHaveBeenCalledWith('activate');
  fireEvent.doubleClick(ball,{button:0});await flush();
  expect(vi.mocked(orbAction).mock.calls.filter(([action])=>action==='activate')).toHaveLength(1);
 });
 it('a drag followed by a click cannot be mistaken for a double-click',async()=>{
  render(<TestOrb/>);const ball=screen.getByRole('button');
  fireEvent.pointerDown(ball,{button:0,pointerId:1,screenX:100,screenY:100});
  fireEvent.pointerMove(ball,{pointerId:1,screenX:150,screenY:100});
  fireEvent.pointerUp(ball,{button:0,pointerId:1,screenX:150,screenY:100});
  fireEvent.pointerDown(ball,{button:0,pointerId:2,screenX:150,screenY:100});
  fireEvent.pointerUp(ball,{button:0,pointerId:2,screenX:150,screenY:100});
  fireEvent.doubleClick(ball,{button:0});await flush();expect(orbAction).not.toHaveBeenCalledWith('activate');
 });
});
