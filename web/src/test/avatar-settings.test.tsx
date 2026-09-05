import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { webcrypto } from 'node:crypto';
import { AvatarSettings } from '@/components/common/AvatarSettings';
import { LocaleProvider } from '@/context/LocaleContext';
import { api } from '@/api/client';
import type { UserProfile } from '@/types/api';

vi.mock('@/context/AuthContext',()=>({useAuth:()=>({setUser:vi.fn()})}));
const profile:UserProfile={userId:'owner',displayName:'Owner',handle:'owner',avatarUrl:'/api/v1/public/avatars/old',bio:null,timezone:'UTC',locale:'zh-CN',profileVersion:1,onboardingCompletedAt:null};
function showAvatar() { const updated=vi.fn();render(<LocaleProvider><AvatarSettings profile={profile} onUpdated={updated} onBusy={vi.fn()} /></LocaleProvider>);return updated; }
function chooseFile(type='image/png') {
  const file=new File([new Uint8Array([1,2,3])],'avatar.png',{type});
  Object.defineProperty(file,'arrayBuffer',{value:async()=>Uint8Array.from([1,2,3]).buffer});
  fireEvent.change(screen.getByLabelText('选择头像文件'),{target:{files:[file]}});return file;
}
beforeEach(()=>{
  vi.restoreAllMocks();localStorage.clear();vi.stubGlobal('crypto',webcrypto);
  Object.defineProperty(URL,'createObjectURL',{configurable:true,value:vi.fn(()=> 'blob:preview')});
  Object.defineProperty(URL,'revokeObjectURL',{configurable:true,value:vi.fn()});
  vi.spyOn(api,'createAvatarUploadIntent').mockResolvedValue({objectId:'new',uploadUrl:'https://storage.invalid/signed',expiresAt:''});
  vi.spyOn(api,'uploadAvatarContent').mockResolvedValue(undefined);
  vi.spyOn(api,'completeAvatarUpload').mockResolvedValue({objectId:'new',uploadStatus:'ready'});
  vi.spyOn(api,'getProfile').mockResolvedValue({...profile,avatarUrl:'/api/v1/public/avatars/new',profileVersion:2});
});
afterEach(()=>vi.unstubAllGlobals());
describe('Avatar settings',()=>{
  it('previews locally then uploads through the application and refreshes the saved profile',async()=>{
    const updated=showAvatar();const file=chooseFile();
    expect(await screen.findByAltText('头像预览')).toHaveAttribute('src','blob:preview');
    expect(api.createAvatarUploadIntent).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button',{name:'保存头像'}));
    await waitFor(()=>expect(updated).toHaveBeenCalledWith(expect.objectContaining({profileVersion:2,avatarUrl:'/api/v1/public/avatars/new'})));
    expect(api.createAvatarUploadIntent).toHaveBeenCalledWith('image/png',3,expect.stringMatching(/^[0-9a-f]{64}$/));
    expect(api.uploadAvatarContent).toHaveBeenCalledWith('new',file);
    expect(api.completeAvatarUpload).toHaveBeenCalledWith('new');
  });
  it('rejects unsupported files before creating an upload',()=>{
    showAvatar();chooseFile('image/svg+xml');expect(screen.getByRole('alert')).toHaveTextContent('5 MB');expect(api.createAvatarUploadIntent).not.toHaveBeenCalled();
  });
  it('keeps the old avatar when validation fails',async()=>{
    vi.mocked(api.completeAvatarUpload).mockRejectedValue(new Error('invalid image'));
    const updated=showAvatar();chooseFile();fireEvent.click(screen.getByRole('button',{name:'保存头像'}));
    expect(await screen.findByRole('alert')).toHaveTextContent('头像更新失败');expect(updated).not.toHaveBeenCalled();expect(api.getProfile).not.toHaveBeenCalled();
  });
  it('removes an avatar and refreshes its profile version',async()=>{
    vi.spyOn(api,'deleteAvatar').mockResolvedValue(undefined);vi.mocked(api.getProfile).mockResolvedValue({...profile,avatarUrl:null,profileVersion:2});
    const updated=showAvatar();fireEvent.click(screen.getByRole('button',{name:'移除头像'}));
    await waitFor(()=>expect(updated).toHaveBeenCalledWith(expect.objectContaining({avatarUrl:null,profileVersion:2})));
  });
});
