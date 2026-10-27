package mirror;

import java.lang.reflect.Field;

/* loaded from: classes2.dex */
public class RefObject<T> {
    private Field field;

    public RefObject(Class<?> cls, Field field) throws NoSuchFieldException {
        Field declaredField = cls.getDeclaredField(field.getName());
        this.field = declaredField;
        declaredField.setAccessible(true);
    }

    public T get(Object obj) {
        try {
            return (T) this.field.get(obj);
        } catch (Exception e) {
            e.printStackTrace();
            return null;
        }
    }

    public boolean set(Object obj, T value) throws IllegalAccessException, IllegalArgumentException {
        try {
            this.field.set(obj, value);
            return true;
        } catch (Exception e) {
            e.printStackTrace();
            return false;
        }
    }
}
