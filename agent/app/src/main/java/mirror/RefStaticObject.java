package mirror;

import java.lang.reflect.Field;

/* loaded from: classes2.dex */
public class RefStaticObject<T> {
    private Field field;

    public RefStaticObject(Class<?> cls, Field field) throws NoSuchFieldException {
        Field declaredField = cls.getDeclaredField(field.getName());
        this.field = declaredField;
        declaredField.setAccessible(true);
    }

    public Class<?> type() {
        return this.field.getType();
    }

    public T get() {
        try {
            return (T) this.field.get(null);
        } catch (Exception e) {
            return null;
        }
    }

    public void set(T obj) throws IllegalAccessException, IllegalArgumentException {
        try {
            this.field.set(null, obj);
        } catch (Exception e) {
        }
    }
}
