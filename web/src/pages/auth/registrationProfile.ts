export const registrationAvatars = [
  { id: 'cat', zh: '小猫', en: 'Cat' },
  { id: 'fox', zh: '狐狸', en: 'Fox' },
  { id: 'panda', zh: '熊猫', en: 'Panda' },
  { id: 'bunny', zh: '兔兔', en: 'Bunny' },
] as const;

export function randomNickname(locale: string): string {
  const adjectives = locale === 'zh-CN'
    ? ['晴天', '星际', '软糖', '幸运', '好奇', '跳舞', '薄荷', '追梦']
    : ['Sunny', 'Cosmic', 'Mellow', 'Lucky', 'Curious', 'Dancing', 'Minty', 'Dreamy'];
  const animals = locale === 'zh-CN'
    ? ['小猫', '狐狸', '熊猫', '兔兔', '海獭', '企鹅', '考拉', '猫头鹰']
    : ['Cat', 'Fox', 'Panda', 'Bunny', 'Otter', 'Penguin', 'Koala', 'Owl'];
  return `${adjectives[Math.floor(Math.random() * adjectives.length)]}${animals[Math.floor(Math.random() * animals.length)]}_${Math.floor(Math.random() * 1679616).toString(36).padStart(4, '0')}`;
}

export const randomAvatarId = () => registrationAvatars[Math.floor(Math.random() * registrationAvatars.length)].id;
